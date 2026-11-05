package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"inbok/internal/account"
	"inbok/internal/repository/postgres/sqlc"
)

var _ account.MemberStorage = (*DB)(nil)

const (
	memberDeactivatedAction = "member-deactivated"
	memberReactivatedAction = "member-reactivated"
)

// ReadMembers implements [account.MemberStorage.ReadMembers].
func (db *DB) ReadMembers(ctx context.Context) ([]account.Member, error) {
	rows, err := sqlc.New(db.pool).ReadMemberUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("read members: %w", err)
	}
	members := make([]account.Member, len(rows))
	for i, row := range rows {
		// Both queries select the same columns, so one mapping serves them.
		members[i] = memberFromRow(sqlc.LockMemberUserRow(row))
	}
	return members, nil
}

// UpdateMember implements [account.MemberStorage.UpdateMember].
func (db *DB) UpdateMember(ctx context.Context, actorID, memberID int64, update func(account.Member) (account.MemberChange, error)) (account.Member, error) {
	var member account.Member
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the member.
		row, err := queries.LockMemberUser(ctx, memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			return account.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock member: %w", err)
		}
		member = memberFromRow(row)

		// 2. Let update decide. Keeping the member writes nothing.
		change, err := update(member)
		if err != nil {
			return err
		}
		var action string
		var deactivatedAt *time.Time
		switch change.Action {
		case account.MemberKeep:
			return nil
		case account.MemberDeactivate:
			action, deactivatedAt = memberDeactivatedAction, &change.At
		case account.MemberReactivate:
			action = memberReactivatedAction
		default:
			return fmt.Errorf("unknown member action %d", change.Action)
		}

		// 3. Save the status. Return the saved time, which pgx truncates to
		// microseconds, so the result equals what a later read finds.
		saved, err := queries.UpdateUserDeactivatedAt(ctx, sqlc.UpdateUserDeactivatedAtParams{
			UserID: memberID, DeactivatedAt: deactivatedAt,
		})
		if err != nil {
			return fmt.Errorf("save member status: %w", err)
		}
		row.DeactivatedAt = saved
		member = memberFromRow(row)
		if change.Action == account.MemberDeactivate {
			// 4. On deactivation, end every way in the member has. The
			// password and factor stay, so reactivating restores the account
			// as it was.
			if err := queries.DeleteUserSessions(ctx, memberID); err != nil {
				return fmt.Errorf("end member sessions: %w", err)
			}
			if _, err := queries.DeleteUserSignInChallenges(ctx, memberID); err != nil {
				return fmt.Errorf("remove member challenges: %w", err)
			}
			if err := queries.DeleteUserEmailVerificationLinks(ctx, memberID); err != nil {
				return fmt.Errorf("remove member verification links: %w", err)
			}
			if _, err := queries.DeleteUserPasswordResetLinks(ctx, memberID); err != nil {
				return fmt.Errorf("remove member reset links: %w", err)
			}
		}

		// 5. Record the event.
		if err := queries.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
			Action: action, ActorUserID: &actorID, TargetUserID: &memberID,
		}); err != nil {
			return fmt.Errorf("record member status: %w", err)
		}
		return nil
	})
	if err != nil {
		return account.Member{}, err
	}
	return member, nil
}

// memberFromRow returns the member a row describes, with times in UTC.
func memberFromRow(row sqlc.LockMemberUserRow) account.Member {
	member := account.Member{
		User:          account.User{ID: row.ID, Email: row.Email, Role: account.Role(row.Role)},
		JoinedAt:      row.CreatedAt.UTC(),
		EmailVerified: row.EmailVerified,
		TOTPEnabled:   row.TotpEnabled,
	}
	if row.DeactivatedAt != nil {
		at := row.DeactivatedAt.UTC()
		member.DeactivatedAt = &at
	}
	return member
}
