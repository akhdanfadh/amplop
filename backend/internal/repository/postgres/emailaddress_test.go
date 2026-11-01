package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// beforeEmailAddressMigration is the version before addresses were lowercased.
const beforeEmailAddressMigration = 15

// The migration lowercases the ASCII letters of every saved address and
// leaves other letters and missing addresses alone.
func TestEmailAddressMigration(t *testing.T) {
	// Given addresses saved in mixed case before the migration.
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), beforeEmailAddressMigration)
	require.NoError(t, err)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())

	admin := verificationUser(t, conn, "Admin@Example.COM")
	verificationUser(t, conn, "ÉLise@Example.FR")
	link := verificationLink(admin, "link", time.Hour)
	link.Address = "Admin@Example.COM"
	insertVerificationLink(t, conn, link)
	_, err = conn.Exec(t.Context(), `INSERT INTO sign_in_attempts (email, client_addr, created_at)
		VALUES ('Admin@Example.COM', '192.0.2.1', now()), (NULL, '192.0.2.1', now())`)
	require.NoError(t, err)
	_, err = conn.Exec(t.Context(), `INSERT INTO system_email (host, port, security, from_address, from_name)
		VALUES ('localhost', 1025, 'none', 'Inbok@Example.COM', 'inbok')`)
	require.NoError(t, err)

	// When the migration runs.
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	// Then each address keeps only its non-ASCII letters' case.
	assert.Equal(t, []string{"admin@example.com", "Élise@example.fr"}, queryStrings[string](t, conn, "SELECT email FROM users ORDER BY id"))
	assert.Equal(t, []string{"admin@example.com"}, queryStrings[string](t, conn, "SELECT address FROM email_verification_links"))
	assert.Equal(t, []*string{new("admin@example.com"), nil}, queryStrings[*string](t, conn, "SELECT email FROM sign_in_attempts ORDER BY id"))
	assert.Equal(t, []string{"inbok@example.com"}, queryStrings[string](t, conn, "SELECT from_address FROM system_email"))
}

// Down restores uniqueness that ignores case and allows uppercase again;
// Up applies again afterwards.
func TestEmailAddressMigrationDown(t *testing.T) {
	// Given a user saved after the migration.
	const uniqueViolation = "23505"
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	verificationUser(t, conn, "admin@example.com")

	// When the migration is undone.
	_, err = provider.DownTo(t.Context(), beforeEmailAddressMigration)
	require.NoError(t, err)

	// Then a case variant of that user's email is refused, and a new
	// uppercase email is allowed.
	_, err = conn.Exec(t.Context(), "INSERT INTO users (email, password_hash, role) VALUES ('ADMIN@example.com', 'unused', 'member')")
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, uniqueViolation, pgErr.Code)
	verificationUser(t, conn, "Other@example.com")

	// And applying the migration again lowercases it.
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"admin@example.com", "other@example.com"}, queryStrings[string](t, conn, "SELECT email FROM users ORDER BY id"))
}

// queryStrings returns the single text column of every row sql returns.
func queryStrings[T string | *string](t *testing.T, conn *pgx.Conn, sql string) []T {
	t.Helper()

	rows, err := conn.Query(t.Context(), sql)
	require.NoError(t, err)
	values, err := pgx.CollectRows(rows, pgx.RowTo[T])
	require.NoError(t, err)
	return values
}
