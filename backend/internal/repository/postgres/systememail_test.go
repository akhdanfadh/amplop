package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/encryption"
	"inbok/internal/systememail"
)

// uniqueViolation is PostgreSQL's error code for a duplicate key.
const uniqueViolation = "23505"

func systemEmailInput() systememail.Input {
	return systememail.Input{
		Host:        "smtp.example.com",
		Port:        587,
		Security:    systememail.SecuritySTARTTLS,
		Username:    "inbok@example.com",
		Password:    "app password",
		FromAddress: "inbok@example.com",
		FromName:    "inbok",
	}
}

func TestSystemEmailStorage(t *testing.T) {
	db, conn := setupStorage(t)
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	service := systememail.NewService(db, ring)

	// Nothing is saved on a new installation.
	settings, err := service.Settings(t.Context())
	require.NoError(t, err)
	assert.Nil(t, settings)

	// The first save stores the password encrypted for this server and account.
	saved, err := service.Save(t.Context(), systemEmailInput())
	require.NoError(t, err)
	record, err := db.ReadSystemEmail(t.Context())
	require.NoError(t, err)
	require.NotNil(t, record)
	require.NotNil(t, record.Password)
	assert.NotContains(t, string(record.Password.Ciphertext), "app password")
	opened, err := ring.Open(record.Password.KeyID, record.Password.Ciphertext, `system-email:"smtp.example.com":587:"inbok@example.com"`)
	require.NoError(t, err)
	assert.Equal(t, "app password", string(opened))
	_, err = ring.Open(record.Password.KeyID, record.Password.Ciphertext, `system-email:"smtp.attacker.example":587:"inbok@example.com"`)
	require.Error(t, err, "the password does not decrypt for another server")
	settings, err = service.Settings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &saved, settings)

	// A save without a password keeps the stored one and clears the tested time.
	_, err = conn.Exec(t.Context(), "UPDATE system_email SET tested_at = now()")
	require.NoError(t, err)
	input := systemEmailInput()
	input.Password, input.FromName = "", "Mail"
	_, err = service.Save(t.Context(), input)
	require.NoError(t, err)
	kept, err := db.ReadSystemEmail(t.Context())
	require.NoError(t, err)
	require.NotNil(t, kept)
	assert.Equal(t, "Mail", kept.FromName)
	assert.Equal(t, record.Password, kept.Password)
	assert.Nil(t, kept.TestedAt)

	// Removing the username removes the password.
	input.Username = ""
	saved, err = service.Save(t.Context(), input)
	require.NoError(t, err)
	assert.False(t, saved.HasPassword)
	cleared, err := db.ReadSystemEmail(t.Context())
	require.NoError(t, err)
	require.NotNil(t, cleared)
	assert.Empty(t, cleared.Username)
	assert.Nil(t, cleared.Password)
}

func TestSystemEmailUpdate(t *testing.T) {
	db, conn := setupStorage(t)
	tested := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	want := systememail.Record{
		Host: "smtp.example.com", Port: 465, Security: systememail.SecurityTLS,
		Username: "inbok", Password: &systememail.Password{KeyID: "1", Ciphertext: []byte("sealed")},
		FromAddress: "inbok@example.com", FromName: "inbok", TestedAt: &tested,
	}

	require.NoError(t, db.UpdateSystemEmail(t.Context(), func(current *systememail.Record) (systememail.Record, error) {
		assert.Nil(t, current)
		return want, nil
	}))
	var got *systememail.Record
	require.NoError(t, db.UpdateSystemEmail(t.Context(), func(current *systememail.Record) (systememail.Record, error) {
		got = current
		return systememail.Record{Host: "localhost", Port: 1025, Security: systememail.SecurityNone, FromAddress: "inbok@localhost", FromName: "inbok"}, nil
	}))
	var rows int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM system_email").Scan(&rows))

	require.NotNil(t, got)
	require.NotNil(t, got.TestedAt)
	assert.True(t, tested.Equal(*got.TestedAt))
	got.TestedAt = want.TestedAt
	assert.Equal(t, &want, got, "the update sees the saved record")
	assert.Equal(t, 1, rows, "saves replace the one row")
	replaced, err := db.ReadSystemEmail(t.Context())
	require.NoError(t, err)
	require.NotNil(t, replaced)
	assert.Equal(t, "localhost", replaced.Host)
	assert.Nil(t, replaced.Password)
}

// Two first saves have no row to lock; the table lock still makes the second
// wait and then see the first one's record.
func TestSystemEmailUpdateSerializes(t *testing.T) {
	db, conn := setupStorage(t)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()
	ready, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	defer releaseFirst()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	first := systememail.Record{Host: "first.example.com", Port: 25, Security: systememail.SecurityNone, FromAddress: "inbok@example.com", FromName: "inbok"}
	var seen *systememail.Record

	go func() {
		firstDone <- db.UpdateSystemEmail(ctx, func(*systememail.Record) (systememail.Record, error) {
			close(ready)
			select {
			case <-release:
				return first, nil
			case <-ctx.Done():
				return systememail.Record{}, ctx.Err()
			}
		})
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		require.FailNow(t, "first save did not reach its callback")
	}
	read, err := db.ReadSystemEmail(ctx)
	require.NoError(t, err, "reads stay available while a save holds the lock")
	assert.Nil(t, read)
	go func() {
		secondDone <- db.UpdateSystemEmail(ctx, func(current *systememail.Record) (systememail.Record, error) {
			seen = current
			return first, nil
		})
	}()
	const waiting = `SELECT EXISTS (
		SELECT 1 FROM pg_locks
		WHERE relation = 'system_email'::regclass AND mode = 'ShareRowExclusiveLock' AND NOT granted
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
	)`
	require.Eventually(t, func() bool {
		var blocked bool
		err := conn.QueryRow(ctx, waiting).Scan(&blocked)
		return err == nil && blocked
	}, setupTestTimeout, 10*time.Millisecond, "second save did not wait on the table lock")
	releaseFirst()

	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.Equal(t, &first, seen)
}

func TestSystemEmailUpdateRejected(t *testing.T) {
	db, _ := setupStorage(t)
	original := systememail.Record{Host: "smtp.example.com", Port: 25, Security: systememail.SecurityNone, FromAddress: "inbok@example.com", FromName: "inbok"}
	require.NoError(t, db.UpdateSystemEmail(t.Context(), func(*systememail.Record) (systememail.Record, error) { return original, nil }))

	err := db.UpdateSystemEmail(t.Context(), func(*systememail.Record) (systememail.Record, error) {
		return systememail.Record{}, systememail.ErrPasswordRequired
	})
	require.ErrorIs(t, err, systememail.ErrPasswordRequired)

	got, err := db.ReadSystemEmail(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &original, got)
}

// A failed commit returns an error and leaves the earlier settings.
func TestSystemEmailCommitFailure(t *testing.T) {
	db, conn := setupStorage(t)
	original := systememail.Record{Host: "smtp.example.com", Port: 25, Security: systememail.SecurityNone, FromAddress: "inbok@example.com", FromName: "inbok"}
	require.NoError(t, db.UpdateSystemEmail(t.Context(), func(*systememail.Record) (systememail.Record, error) { return original, nil }))
	_, err := conn.Exec(t.Context(), `
  CREATE FUNCTION reject_system_email() RETURNS trigger LANGUAGE plpgsql AS
  $$ BEGIN RAISE EXCEPTION 'forced system email failure'; END $$;
  CREATE CONSTRAINT TRIGGER reject_system_email AFTER UPDATE ON system_email
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_system_email();`)
	require.NoError(t, err)

	err = db.UpdateSystemEmail(t.Context(), func(current *systememail.Record) (systememail.Record, error) {
		next := *current
		next.Host = "mail.example.com"
		return next, nil
	})
	require.ErrorContains(t, err, "forced system email failure")

	got, err := db.ReadSystemEmail(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &original, got)
}

// The table repeats the feature's consistency rules for writes that skip it.
func TestSystemEmailChecks(t *testing.T) {
	conn := migrated(t)
	const insert = `INSERT INTO system_email (host, port, security, username, password_key_id, password_ciphertext, from_address, from_name)
		VALUES ('smtp.example.com', $1, $2, $3, $4, $5, 'inbok@example.com', 'inbok')`
	user, empty, key, none := "inbok", "", "1", ""
	sealed := []byte("sealed")

	cases := []struct {
		name       string
		port       int
		security   string
		username   *string
		keyID      *string
		sealed     []byte
		constraint string
	}{
		{"port zero", 0, "none", nil, nil, nil, "system_email_port_check"},
		{"port too large", 65536, "none", nil, nil, nil, "system_email_port_check"},
		{"unknown security", 465, "ssl", nil, nil, nil, "system_email_security_check"},
		{"empty username", 587, "starttls", &empty, &key, sealed, "system_email_username_check"},
		{"empty key ID", 587, "starttls", &user, &none, sealed, "system_email_password_key_id_check"},
		{"empty ciphertext", 587, "starttls", &user, &key, []byte{}, "system_email_password_ciphertext_check"},
		{"key without ciphertext", 587, "starttls", &user, &key, nil, "system_email_password_sealed"},
		{"username without password", 587, "starttls", &user, nil, nil, "system_email_sign_in_paired"},
		{"password without username", 587, "starttls", nil, &key, sealed, "system_email_sign_in_paired"},
		{"username without TLS", 25, "none", &user, &key, sealed, "system_email_sign_in_encrypted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := conn.Exec(t.Context(), insert, c.port, c.security, c.username, c.keyID, c.sealed)

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, checkViolation, pgErr.Code)
			assert.Equal(t, c.constraint, pgErr.ConstraintName)
		})
	}

	_, err := conn.Exec(t.Context(), insert, 587, "starttls", &user, &key, sealed)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), insert, 25, "none", nil, nil, nil)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr, "a second row is rejected")
	assert.Equal(t, uniqueViolation, pgErr.Code)
	_, err = conn.Exec(t.Context(), "INSERT INTO system_email (id, host, port, security, from_address, from_name) VALUES (false, 'localhost', 25, 'none', 'inbok@localhost', 'inbok')")
	require.ErrorAs(t, err, &pgErr, "only the true key is allowed")
	assert.Equal(t, checkViolation, pgErr.Code)
}

func TestSystemEmailMigrationDown(t *testing.T) {
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), 12)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
}
