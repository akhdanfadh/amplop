package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/registration"
)

// systemEmailTest is whether saved system email settings passed a test.
type systemEmailTest int

const (
	untested systemEmailTest = iota
	tested
)

// saveSystemEmail stores system email settings with the given test result.
func saveSystemEmail(t *testing.T, conn *pgx.Conn, test systemEmailTest) {
	t.Helper()

	_, err := conn.Exec(t.Context(), `INSERT INTO system_email (host, port, security, from_address, from_name, tested_at)
		VALUES ('localhost', 1025, 'none', 'inbok@localhost', 'inbok', CASE WHEN $1 THEN now() END)
		ON CONFLICT (id) DO UPDATE SET tested_at = EXCLUDED.tested_at`, test == tested)
	require.NoError(t, err)
}

func TestRegistrationStorage(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	service := registration.NewService(db)

	// A new installation starts closed, without system email.
	settings, err := service.Settings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusClosed}, settings)

	// Neither missing nor untested settings can open it.
	_, err = service.Save(t.Context(), admin, registration.StateOpen)
	require.ErrorIs(t, err, registration.ErrSystemEmailNotTested)
	saveSystemEmail(t, conn, untested)
	_, err = service.Save(t.Context(), admin, registration.StateOpen)
	require.ErrorIs(t, err, registration.ErrSystemEmailNotTested)

	// Tested settings can.
	saveSystemEmail(t, conn, tested)
	saved, err := service.Save(t.Context(), admin, registration.StateOpen)
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusOpen, SystemEmailTested: true}, saved)

	// Settings that lose their test pause it.
	saveSystemEmail(t, conn, untested)
	settings, err = service.Settings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusPaused}, settings)

	// Closing needs no tested settings.
	saved, err = service.Save(t.Context(), admin, registration.StateClosed)
	require.NoError(t, err)
	assert.Equal(t, registration.Settings{Status: registration.StatusClosed}, saved)

	// Each save is audited with its administrator and no account; the
	// refused ones are not.
	assert.Equal(t, []auditEvent{
		{Action: "registration-opened", Actor: &admin},
		{Action: "registration-closed", Actor: &admin},
	}, auditEvents(t, conn))
}

func TestRegistrationUpdateRollsBack(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	refused := errors.New("refused")

	err := db.UpdateRegistration(t.Context(), admin, func(registration.Record) (registration.State, error) {
		return "", refused
	})

	require.ErrorIs(t, err, refused)
	record, err := db.ReadRegistration(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Record{State: registration.StateClosed}, record)
	assert.Empty(t, auditEvents(t, conn))
}

// An open that waits for a system email save sees the save's result: new
// settings have not passed a test, so registration stays closed.
func TestRegistrationOpenWaitsForSave(t *testing.T) {
	db, conn := setupStorage(t)
	admin := systemEmailAdmin(t, conn)
	saveSystemEmail(t, conn, tested)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	// Given a system email save that has cleared the tested time but not
	// committed.
	save, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer save.Rollback(context.Background())
	_, err = save.Exec(ctx, "UPDATE system_email SET tested_at = NULL")
	require.NoError(t, err)

	// When an administrator opens registration meanwhile.
	opened := make(chan error, 1)
	go func() {
		_, err := registration.NewService(db).Save(ctx, admin, registration.StateOpen)
		opened <- err
	}()
	const waiting = `SELECT EXISTS (
		SELECT 1 FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'
	)`
	require.Eventually(t, func() bool {
		var blocked bool
		err := db.pool.QueryRow(ctx, waiting).Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "the open did not wait for the save")
	require.NoError(t, save.Commit(ctx))

	// Then the open is refused and nothing is recorded.
	require.ErrorIs(t, <-opened, registration.ErrSystemEmailNotTested)
	record, err := db.ReadRegistration(t.Context())
	require.NoError(t, err)
	assert.Equal(t, registration.Record{State: registration.StateClosed}, record)
	assert.Empty(t, auditEvents(t, conn))
}

func TestRegistrationChecks(t *testing.T) {
	conn := migrated(t)
	var pgErr *pgconn.PgError

	_, err := conn.Exec(t.Context(), "INSERT INTO registration (open) VALUES (true)")
	require.ErrorAs(t, err, &pgErr, "the table holds one row")
	assert.Equal(t, uniqueViolation, pgErr.Code)

	_, err = conn.Exec(t.Context(), "INSERT INTO registration (id) VALUES (false)")
	require.ErrorAs(t, err, &pgErr, "only the true key is allowed")
	assert.Equal(t, checkViolation, pgErr.Code)
}
