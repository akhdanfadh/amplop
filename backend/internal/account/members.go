package account

import (
	"context"
	"errors"
	"time"
)

// ErrAdminTarget means a change targets an administrator, the acting one
// included. Only members can be deactivated or reactivated.
var ErrAdminTarget = errors.New("administrators cannot be deactivated or reactivated")

// Member is an account as administrators see it in the member list, which
// includes administrators. It holds nothing about the account's mail or
// activity.
type Member struct {
	User
	JoinedAt      time.Time
	EmailVerified bool
	TOTPEnabled   bool       // a confirmed factor; an unconfirmed enrollment is not one
	DeactivatedAt *time.Time // nil: the account is active
}

// MemberAction is the status change [MemberStorage.UpdateMember] saves.
type MemberAction int

const (
	MemberKeep       MemberAction = iota // nothing is saved
	MemberDeactivate                     // the member is deactivated at [MemberChange.At]
	MemberReactivate                     // the member is active again
)

// MemberChange is what [MemberStorage.UpdateMember] saves. The zero value
// saves nothing.
type MemberChange struct {
	Action MemberAction
	At     time.Time // when the deactivation happens, for MemberDeactivate
}

// MemberStorage keeps accounts' status for administrators.
type MemberStorage interface {
	// ReadMembers lists every account, oldest first.
	ReadMembers(ctx context.Context) ([]Member, error)

	// UpdateMember locks the member's account, reads it, and calls update.
	// For MemberDeactivate it saves the time and deletes the member's
	// sessions, sign-in challenges, and email verification links; for
	// MemberReactivate it clears the time. Either change records an audit
	// event with actorID as actor and the member as target, all in one
	// transaction. It returns the member as saved. A missing member returns
	// [ErrUserNotFound]. An error from update rolls back and is returned
	// unchanged.
	UpdateMember(ctx context.Context, actorID, memberID int64, update func(Member) (MemberChange, error)) (Member, error)
}

// Members lets administrators list accounts and deactivate or reactivate
// members.
type Members struct {
	storage MemberStorage
	now     func() time.Time
}

// NewMembers returns a Members service that saves through storage and dates
// deactivations with now.
func NewMembers(storage MemberStorage, now func() time.Time) *Members {
	return &Members{storage: storage, now: now}
}

// List returns every account, oldest first.
func (m *Members) List(ctx context.Context) ([]Member, error) {
	return m.storage.ReadMembers(ctx)
}

// Deactivate deactivates the member with memberID on behalf of the
// administrator with actorID, ending the member's sessions, and returns the
// updated member. A member already deactivated is returned unchanged. It
// returns [ErrAdminTarget] for an administrator and [ErrUserNotFound] for
// an unknown account.
func (m *Members) Deactivate(ctx context.Context, actorID, memberID int64) (Member, error) {
	return m.storage.UpdateMember(ctx, actorID, memberID, func(member Member) (MemberChange, error) {
		if member.Role != RoleMember {
			return MemberChange{}, ErrAdminTarget
		}
		if member.DeactivatedAt != nil {
			return MemberChange{}, nil
		}
		return MemberChange{Action: MemberDeactivate, At: m.now().UTC()}, nil
	})
}

// Reactivate makes the member with memberID active again on behalf of the
// administrator with actorID and returns the updated member. An active
// member is returned unchanged. It returns [ErrAdminTarget] for an
// administrator and [ErrUserNotFound] for an unknown account.
func (m *Members) Reactivate(ctx context.Context, actorID, memberID int64) (Member, error) {
	return m.storage.UpdateMember(ctx, actorID, memberID, func(member Member) (MemberChange, error) {
		if member.Role != RoleMember {
			return MemberChange{}, ErrAdminTarget
		}
		if member.DeactivatedAt == nil {
			return MemberChange{}, nil
		}
		return MemberChange{Action: MemberReactivate}, nil
	})
}
