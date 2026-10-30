package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditEventUser adds the account audit events refer to in a test.
func auditEventUser(t *testing.T, conn *pgx.Conn) int64 {
	t.Helper()

	var id int64
	require.NoError(t, conn.QueryRow(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('admin@example.com', 'unused', 'admin') RETURNING id").Scan(&id))
	return id
}

// An event names an actor, a target, or both, never neither.
func TestAuditEventChecks(t *testing.T) {
	conn := migrated(t)
	id := auditEventUser(t, conn)
	const insert = "INSERT INTO audit_events (action, actor_user_id, target_user_id) VALUES ('test', $1, $2)"

	for _, c := range []struct {
		name          string
		actor, target *int64
	}{
		{"actor only", &id, nil},
		{"target only", nil, &id},
		{"both", &id, &id},
	} {
		_, err := conn.Exec(t.Context(), insert, c.actor, c.target)
		require.NoError(t, err, c.name)
	}
	_, err := conn.Exec(t.Context(), insert, nil, nil)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, checkViolation, pgErr.Code)
	assert.Equal(t, "audit_events_party", pgErr.ConstraintName)
}

// Splitting the parties keeps an earlier recovery event as a target with no
// actor. Undoing it keeps events with a target under the old column and
// removes the ones the old table cannot hold.
func TestAuditEventMigration(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	id := auditEventUser(t, conn)
	_, err = provider.DownTo(t.Context(), 13)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), "INSERT INTO audit_events (action, user_id) VALUES ('admin-recovered', $1)", id)
	require.NoError(t, err)

	// Up: the recovery event becomes a target with no actor.
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []auditEvent{{Action: "admin-recovered", Target: &id}}, auditEvents(t, conn))

	// Down: a settings save and the actor of a two-party event are lost.
	_, err = conn.Exec(t.Context(), `INSERT INTO audit_events (action, actor_user_id, target_user_id)
		VALUES ('system-email-saved', $1, NULL), ('both', $1, $1)`, id)
	require.NoError(t, err)
	_, err = provider.DownTo(t.Context(), 13)
	require.NoError(t, err)
	rows, err := conn.Query(t.Context(), "SELECT action, user_id FROM audit_events ORDER BY id")
	require.NoError(t, err)
	type oldEvent struct {
		Action string
		UserID int64
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[oldEvent])
	require.NoError(t, err)
	assert.Equal(t, []oldEvent{{"admin-recovered", id}, {"both", id}}, events)
}
