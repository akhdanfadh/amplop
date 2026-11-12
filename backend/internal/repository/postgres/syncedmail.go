package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"inbok/internal/mailsync"
	"inbok/internal/repository/postgres/sqlc"
)

// errStateRange means a sync state's generation, or a change's number of
// earlier messages to delete, is negative or past what an integer column
// or parameter holds.
var errStateRange = errors.New("sync state number is out of storage's range")

// errVersionRange means a message's provider version is 0, which no
// provider gives, or past what a bigint column holds.
var errVersionRange = errors.New("message version is out of storage's range")

// storedVersion returns version as the provider_version column holds it, or
// [errVersionRange].
func storedVersion(version uint64) (int64, error) {
	if version == 0 || version > math.MaxInt64 {
		return 0, errVersionRange
	}
	return int64(version), nil
}

// UpdateSyncedMail locks accountID's sync state, calls update with it, and
// writes the change update returns, in one transaction: the mailboxes, the
// deletes, the saves with their mailboxes, the updates, the earlier
// generations' deletes, the conversations of every thread these touched,
// the job deleting the deleted messages' content,
// and the state. It returns what it wrote,
// [mailsync.ErrNotFound] when the account has no sync state, and
// [mailsync.ErrUnknownMailbox] when a saved or updated message names a
// mailbox the account does not have.
func (db *DB) UpdateSyncedMail(ctx context.Context, accountID int64, update func(mailsync.SyncState) (mailsync.Change, error)) (mailsync.Written, error) {
	return db.updateSyncedMail(ctx, accountID, func(_ *sqlc.Queries, current mailsync.SyncState) (mailsync.Change, error) {
		return update(current)
	})
}

// updateSyncedMail is [DB.UpdateSyncedMail] whose update also gets the
// transaction's queries, to read what the change depends on under the
// sync state's lock.
func (db *DB) updateSyncedMail(ctx context.Context, accountID int64, update func(*sqlc.Queries, mailsync.SyncState) (mailsync.Change, error)) (mailsync.Written, error) {
	var written mailsync.Written
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the state, so two steps for the account write one after
		// the other and each sees what the one before saved.
		row, err := queries.LockSyncState(ctx, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return mailsync.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sync state: %w", err)
		}
		current := syncStateFromRow(row)

		// 2. Let update decide, and refuse a state the columns cannot hold
		// before writing anything stamped with it.
		change, err := update(queries, current)
		if err != nil {
			return err
		}
		state, err := syncStateParams(accountID, change.State)
		if err != nil {
			return err
		}
		if change.DeleteEarlier < 0 || change.DeleteEarlier > math.MaxInt32 {
			return errStateRange
		}

		// 3. Write the mailboxes before the messages that name them.
		if err := writeMailboxes(ctx, tx, accountID, change.Mailboxes); err != nil {
			return err
		}

		// 4. Delete before saving. A message both deleted and saved is only saved,
		// in place, so it keeps its ID however often the change repeats.
		saves, saved := lastSaves(change.Saved)
		deletes := slices.DeleteFunc(slices.Clone(change.Deleted), func(providerID string) bool {
			return saved[providerID]
		})
		var threads []string
		var deletedBlobs []uuid.UUID
		if len(deletes) > 0 {
			params := sqlc.DeleteMessagesParams{ConnectedAccountID: accountID, ProviderMessageIds: deletes}
			deleted, err := queries.DeleteMessages(ctx, params)
			if err != nil {
				return fmt.Errorf("delete messages: %w", err)
			}
			threads = append(threads, deleted.ThreadIds...)
			deletedBlobs = deleted.BlobIds
		}
		if err := saveMessages(ctx, tx, accountID, state.SyncGeneration, saves); err != nil {
			return err
		}
		for _, message := range saves {
			threads = append(threads, message.ThreadID)
		}
		updated, err := updateMessages(ctx, tx, accountID, change.Updated)
		if err != nil {
			return err
		}
		threads = append(threads, updated...)

		// 5. Delete what earlier generations saved only after this one's
		// saves, which keep the messages they name.
		var earlierDeleted int64
		if change.DeleteEarlier > 0 {
			params := sqlc.DeleteEarlierMessagesParams{
				ConnectedAccountID: accountID,
				SyncGeneration:     state.SyncGeneration,
				MaxDeleted:         int32(change.DeleteEarlier),
			}
			earlier, err := queries.DeleteEarlierMessages(ctx, params)
			if err != nil {
				return fmt.Errorf("delete earlier messages: %w", err)
			}
			earlierDeleted = earlier.Deleted
			threads = append(threads, earlier.ThreadIds...)
			deletedBlobs = append(deletedBlobs, earlier.BlobIds...)
		}

		// 6. Derive the touched threads' conversations from the messages
		// they have now, which every step above has written.
		if err := deriveConversations(ctx, tx, accountID, uniqueStrings(threads)); err != nil {
			return err
		}

		// 7. Save the job that deletes the deleted messages' blobs. A worker
		// sees it only once this commits, and a rollback drops it, so no
		// row ever names a deleted blob (ADR009).
		if err := db.deleteBlobsAfter(ctx, tx, accountID, deletedBlobs); err != nil {
			return err
		}

		if err := queries.UpdateSyncState(ctx, state); err != nil {
			return fmt.Errorf("update sync state: %w", err)
		}
		written.State = change.State
		written.State.AccountID = accountID
		written.EarlierDeleted = int(earlierDeleted)
		return nil
	})
	if err != nil {
		return mailsync.Written{}, err
	}
	return written, nil
}

// writeMailboxes makes mailboxes accountID's mailboxes in tx: it adds or
// updates each, and deletes the ones not listed. nil leaves them.
func writeMailboxes(ctx context.Context, tx pgx.Tx, accountID int64, mailboxes []mailsync.Mailbox) error {
	if mailboxes == nil {
		return nil
	}
	queries := sqlc.New(tx)
	providerIDs := make([]string, 0, len(mailboxes))
	params := make([]sqlc.UpsertMailboxParams, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		providerIDs = append(providerIDs, mailbox.ProviderID)
		p := sqlc.UpsertMailboxParams{
			ConnectedAccountID: accountID,
			ProviderMailboxID:  mailbox.ProviderID,
			Name:               mailbox.Name,
			MadeBy:             string(mailbox.MadeBy),
		}
		if mailbox.Role != mailsync.RoleNone {
			role := string(mailbox.Role)
			p.Role = &role
		}
		params = append(params, p)
	}

	deleted := sqlc.DeleteUnlistedMailboxesParams{ConnectedAccountID: accountID, ProviderMailboxIds: providerIDs}
	if err := queries.DeleteUnlistedMailboxes(ctx, deleted); err != nil {
		return fmt.Errorf("delete unlisted mailboxes: %w", err)
	}
	var batchErr error
	queries.UpsertMailbox(ctx, params).Exec(func(_ int, err error) {
		batchErr = errors.Join(batchErr, err)
	})
	if batchErr != nil {
		return fmt.Errorf("save mailboxes: %w", batchErr)
	}
	return nil
}

// lastSaves returns saved with one message per ProviderID, the last one
// given, in the order of those last ones, and the set of their ProviderIDs.
func lastSaves(saved []mailsync.Message) ([]mailsync.Message, map[string]bool) {
	last := make(map[string]int, len(saved))
	for i, message := range saved {
		last[message.ProviderID] = i
	}
	saves := make([]mailsync.Message, 0, len(last))
	providerIDs := make(map[string]bool, len(last))
	for i, message := range saved {
		if last[message.ProviderID] == i {
			saves = append(saves, message)
			providerIDs[message.ProviderID] = true
		}
	}
	return saves, providerIDs
}

// saveMessages adds or updates accountID's messages in tx as saved by
// generation, and replaces their mailboxes. Each ProviderID appears once.
// It returns [mailsync.ErrUnknownMailbox] when one names a mailbox the
// account does not have.
func saveMessages(ctx context.Context, tx pgx.Tx, accountID int64, generation int32, messages []mailsync.Message) error {
	if len(messages) == 0 {
		return nil
	}
	queries := sqlc.New(tx)

	// 1. Save the messages, collecting their IDs, new or kept.
	params := make([]sqlc.UpsertMessageParams, 0, len(messages))
	for _, message := range messages {
		p, err := messageParams(accountID, generation, message)
		if err != nil {
			return err
		}
		params = append(params, p)
	}
	ids := make([]uuid.UUID, len(messages))
	var batchErr error
	queries.UpsertMessage(ctx, params).QueryRow(func(i int, id uuid.UUID, err error) {
		ids[i] = id
		batchErr = errors.Join(batchErr, err)
	})
	if batchErr != nil {
		return fmt.Errorf("save messages: %w", batchErr)
	}

	// 2. Replace their mailboxes with the ones each names now.
	mailboxes := make([][]string, len(messages))
	for i, message := range messages {
		mailboxes[i] = message.MailboxProviderIDs
	}
	return replaceMailboxes(ctx, tx, accountID, ids, mailboxes)
}

// updateMessages sets the flags, version, and mailboxes of accountID's
// messages that updates name, in tx, skipping those it does not have and
// those it holds at a newer version; when one ProviderID appears more than
// once, the last wins. It returns the threads of the messages it updated,
// [errVersionRange] for an update without a version, and
// [mailsync.ErrUnknownMailbox] when one names a mailbox the account does
// not have.
func updateMessages(ctx context.Context, tx pgx.Tx, accountID int64, updates []mailsync.MessageUpdate) ([]string, error) {
	if len(updates) == 0 {
		return nil, nil
	}
	queries := sqlc.New(tx)

	// 1. Set the flags, keeping each ProviderID's last update, and learn
	// which messages the account has at a version no newer.
	last := make(map[string]mailsync.MessageUpdate, len(updates))
	params := sqlc.UpdateMessageFlagsParams{ConnectedAccountID: accountID}
	for _, update := range updates {
		if _, ok := last[update.ProviderID]; !ok {
			params.ProviderMessageIds = append(params.ProviderMessageIds, update.ProviderID)
		}
		last[update.ProviderID] = update
	}
	for _, providerID := range params.ProviderMessageIds {
		version, err := storedVersion(last[providerID].Version)
		if err != nil {
			return nil, err
		}
		params.Seen = append(params.Seen, last[providerID].Seen)
		params.Flagged = append(params.Flagged, last[providerID].Flagged)
		params.ProviderVersions = append(params.ProviderVersions, version)
	}
	rows, err := queries.UpdateMessageFlags(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("update message flags: %w", err)
	}

	// 2. Replace the mailboxes of those it has.
	ids := make([]uuid.UUID, len(rows))
	mailboxes := make([][]string, len(rows))
	threads := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		mailboxes[i] = last[row.ProviderMessageID].MailboxProviderIDs
		threads[i] = row.ProviderThreadID
	}
	return threads, replaceMailboxes(ctx, tx, accountID, ids, mailboxes)
}

// replaceMailboxes makes mailboxes[i] the mailboxes of accountID's message
// with ids[i], in tx. It returns [mailsync.ErrUnknownMailbox] when one names
// a mailbox the account does not have.
func replaceMailboxes(ctx context.Context, tx pgx.Tx, accountID int64, ids []uuid.UUID, mailboxes [][]string) error {
	if len(ids) == 0 {
		return nil
	}
	queries := sqlc.New(tx)

	// 1. Drop the memberships they had.
	if err := queries.DeleteMessageMailboxes(ctx, ids); err != nil {
		return fmt.Errorf("delete message mailboxes: %w", err)
	}

	// 2. Insert the ones they name now.
	var pairs sqlc.InsertMessageMailboxesParams
	pairs.ConnectedAccountID = accountID
	for i, providerIDs := range mailboxes {
		// A provider listing a mailbox twice for one message names one membership.
		for _, providerID := range uniqueStrings(providerIDs) {
			pairs.MessageIds = append(pairs.MessageIds, ids[i])
			pairs.ProviderMailboxIds = append(pairs.ProviderMailboxIds, providerID)
		}
	}
	if len(pairs.MessageIds) == 0 {
		return nil
	}
	inserted, err := queries.InsertMessageMailboxes(ctx, pairs)
	if err != nil {
		return fmt.Errorf("insert message mailboxes: %w", err)
	}

	// 3. A pair whose mailbox the account lacks inserted nothing.
	if inserted != int64(len(pairs.MessageIds)) {
		return mailsync.ErrUnknownMailbox
	}
	return nil
}

// SyncState returns accountID's sync state, or [mailsync.ErrNotFound] when
// it has none.
func (db *DB) SyncState(ctx context.Context, accountID int64) (mailsync.SyncState, error) {
	row, err := sqlc.New(db.pool).ReadSyncState(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailsync.SyncState{}, mailsync.ErrNotFound
	}
	if err != nil {
		return mailsync.SyncState{}, fmt.Errorf("read sync state: %w", err)
	}
	return syncStateFromRow(sqlc.LockSyncStateRow(row)), nil
}

// OldestMessage returns when the provider received accountID's oldest
// message, and false when it has none.
func (db *DB) OldestMessage(ctx context.Context, accountID int64) (time.Time, bool, error) {
	row, err := sqlc.New(db.pool).OldestMessage(ctx, accountID)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("find oldest message: %w", err)
	}
	if !row.Found {
		return time.Time{}, false, nil
	}
	return row.Oldest.UTC(), true, nil
}

// DueSyncs returns, in account ID order, the accounts whose member is
// active and which need no reconnect, and that are complete, waiting,
// running, or failed at or before failedBefore.
func (db *DB) DueSyncs(ctx context.Context, failedBefore time.Time) ([]mailsync.DueSync, error) {
	rows, err := sqlc.New(db.pool).ListDueSyncs(ctx, &failedBefore)
	if err != nil {
		return nil, fmt.Errorf("list due syncs: %w", err)
	}
	due := make([]mailsync.DueSync, 0, len(rows))
	for _, row := range rows {
		sync := mailsync.DueSync{AccountID: row.ConnectedAccountID, Status: mailsync.Status(row.Status), Position: row.Position}
		if row.Failure != nil {
			sync.Failure = mailsync.Failure(*row.Failure)
		}
		due = append(due, sync)
	}
	return due, nil
}

// ConfirmSynced records each of confirmed as its account's last sync time,
// where the account is still complete at the position confirmed with no
// failure, and the time moves forward.
func (db *DB) ConfirmSynced(ctx context.Context, confirmed []mailsync.Confirmed) error {
	params := sqlc.ConfirmSyncedParams{
		ConnectedAccountIds: make([]int64, len(confirmed)),
		Positions:           make([]string, len(confirmed)),
		CheckedAts:          make([]time.Time, len(confirmed)),
	}
	for i, c := range confirmed {
		params.ConnectedAccountIds[i] = c.AccountID
		params.Positions[i] = c.Position
		params.CheckedAts[i] = c.At
	}
	if err := sqlc.New(db.pool).ConfirmSynced(ctx, params); err != nil {
		return fmt.Errorf("confirm synced: %w", err)
	}
	return nil
}

// Reports returns the reports of those of accountIDs that have a sync
// state, in account ID order, a running full sync's progress counted from
// the messages stamped with its generation.
func (db *DB) Reports(ctx context.Context, accountIDs []int64) ([]mailsync.Report, error) {
	rows, err := sqlc.New(db.pool).ListSyncReports(ctx, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("list sync reports: %w", err)
	}
	reports := make([]mailsync.Report, 0, len(rows))
	for _, row := range rows {
		reports = append(reports, reportFromRow(row))
	}
	return reports, nil
}

// RequestSync locks userID's account accountID's sync state, calls request
// with what it finds, records the time request returns as the account's
// request time, and saves the account's sync run unless one is already
// queued or running, in one transaction. It returns the account's report
// after, and [mailsync.ErrNotFound] when userID has no account accountID.
func (db *DB) RequestSync(ctx context.Context, userID, accountID int64, request func(mailsync.SyncRequest) (time.Time, error)) (mailsync.Report, error) {
	if db.workflows == nil {
		return mailsync.Report{}, errNoWorkflows
	}
	var report mailsync.Report
	err := pgx.BeginTxFunc(ctx, db.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)

		// 1. Lock the account, so its need to reconnect holds until this
		// commits; first, as a disconnect locks it before the sync state.
		needsReconnect, err := queries.LockRequestAccount(ctx, sqlc.LockRequestAccountParams{ID: accountID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return mailsync.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock request account: %w", err)
		}

		// 2. Lock the state, so two requests count the limit one after the
		// other.
		requestedAt, err := queries.LockSyncRequest(ctx, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return mailsync.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock sync request: %w", err)
		}

		// 3. Let request decide, and record its time.
		at, err := request(mailsync.SyncRequest{NeedsReconnect: needsReconnect, RequestedAt: inUTC(requestedAt)})
		if err != nil {
			return err
		}
		params := sqlc.UpdateSyncRequestedAtParams{ConnectedAccountID: accountID, SyncRequestedAt: &at}
		if err := queries.UpdateSyncRequestedAt(ctx, params); err != nil {
			return fmt.Errorf("record sync request: %w", err)
		}

		// 4. Save the run. One already queued or running keeps its place,
		// and its poll's recheck reads the time just recorded.
		if _, err := db.workflows.EnqueueSync(ctx, tx, accountID); err != nil {
			return fmt.Errorf("save sync: %w", err)
		}

		// 5. Read the report the member is answered with.
		rows, err := queries.ListSyncReports(ctx, []int64{accountID})
		if err != nil {
			return fmt.Errorf("list sync reports: %w", err)
		}
		if len(rows) == 0 {
			return mailsync.ErrNotFound
		}
		report = reportFromRow(rows[0])
		return nil
	})
	if err != nil {
		return mailsync.Report{}, err
	}
	return report, nil
}

// reportFromRow converts a row to a report.
func reportFromRow(row sqlc.ListSyncReportsRow) mailsync.Report {
	report := mailsync.Report{
		AccountID:     row.ConnectedAccountID,
		Status:        mailsync.Status(row.Status),
		MessagesSoFar: int(row.MessagesSoFar),
		FailedAt:      inUTC(row.FailedAt),
		LastSyncedAt:  inUTC(row.LastSyncedAt),
		RequestedAt:   inUTC(row.SyncRequestedAt),
	}
	if row.Failure != nil {
		report.Failure = mailsync.Failure(*row.Failure)
	}
	return report
}

// inUTC returns a copy of at in UTC, or nil for nil.
func inUTC(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	utc := at.UTC()
	return &utc
}

// uniqueStrings returns values without repeats, in their first order.
func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique
}

// storedAddress is how a message's address lists are kept, one JSON
// object per [mailsync.Address].
type storedAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// messageParams returns message as the upsert's parameters for accountID's
// generation.
func messageParams(accountID int64, generation int32, message mailsync.Message) (sqlc.UpsertMessageParams, error) {
	version, err := storedVersion(message.Version)
	if err != nil {
		return sqlc.UpsertMessageParams{}, err
	}
	var lists [4][]byte
	for i, addresses := range [][]mailsync.Address{message.From, message.ReplyTo, message.To, message.CC} {
		raw, err := encodeAddresses(addresses)
		if err != nil {
			return sqlc.UpsertMessageParams{}, err
		}
		lists[i] = raw
	}
	return sqlc.UpsertMessageParams{
		ConnectedAccountID: accountID,
		ProviderMessageID:  message.ProviderID,
		ProviderThreadID:   message.ThreadID,
		InternalDate:       message.InternalDate,
		Subject:            message.Subject,
		FromAddresses:      lists[0],
		ReplyToAddresses:   lists[1],
		ToAddresses:        lists[2],
		CcAddresses:        lists[3],
		Preview:            message.Preview,
		Seen:               message.Seen,
		Flagged:            message.Flagged,
		InternetMessageID:  message.MessageID,
		InReplyTo:          orEmpty(message.InReplyTo),
		InternetReferences: orEmpty(message.References),
		SyncGeneration:     generation,
		ProviderVersion:    version,
	}, nil
}

// encodeAddresses returns addresses as messages keep them, a JSON array of
// [storedAddress].
func encodeAddresses(addresses []mailsync.Address) ([]byte, error) {
	stored := make([]storedAddress, 0, len(addresses))
	for _, a := range addresses {
		stored = append(stored, storedAddress(a))
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("encode addresses: %w", err)
	}
	return raw, nil
}

// orEmpty returns values, or an empty list for nil.
func orEmpty(values []string) []string {
	// pgx sends a nil slice as NULL, which the columns refuse.
	if values == nil {
		return []string{}
	}
	return values
}

// syncStateParams returns state as accountID's update parameters. It returns
// [errStateRange] for a generation the column cannot hold.
func syncStateParams(accountID int64, state mailsync.SyncState) (sqlc.UpdateSyncStateParams, error) {
	if state.Generation < 0 || state.Generation > math.MaxInt32 {
		return sqlc.UpdateSyncStateParams{}, errStateRange
	}
	params := sqlc.UpdateSyncStateParams{
		ConnectedAccountID: accountID,
		Status:             string(state.Status),
		SyncGeneration:     int32(state.Generation),
		Position:           state.Position,
		FailedAt:           state.FailedAt,
		LastSyncedAt:       state.LastSyncedAt,
	}
	if state.Failure != "" {
		failure := string(state.Failure)
		params.Failure = &failure
	}
	return params, nil
}

// syncStateFromRow converts a row to a sync state.
func syncStateFromRow(row sqlc.LockSyncStateRow) mailsync.SyncState {
	state := mailsync.SyncState{
		AccountID:  row.ConnectedAccountID,
		Status:     mailsync.Status(row.Status),
		Generation: int(row.SyncGeneration),
		Position:   row.Position,
	}
	if row.Failure != nil {
		state.Failure = mailsync.Failure(*row.Failure)
	}
	if row.FailedAt != nil {
		at := row.FailedAt.UTC()
		state.FailedAt = &at
	}
	if row.LastSyncedAt != nil {
		at := row.LastSyncedAt.UTC()
		state.LastSyncedAt = &at
	}
	return state
}
