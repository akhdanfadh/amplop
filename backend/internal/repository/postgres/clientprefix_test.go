package postgres

import (
	"net/netip"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/clientprefix"
)

// beforeClientPrefixMigration is the version before sign-in and registration
// attempts counted a client by its prefix.
const beforeClientPrefixMigration = 21

// savedAddrs are addresses the throttles saved before the migration: IPv4
// addresses unmapped, IPv6 addresses without a zone.
var savedAddrs = []string{"192.0.2.1", "2001:db8:1:2::1", "2001:db8:1:2:ffff::9", "2001:db8:abcd::"}

// The migration turns each saved address into the prefix [clientprefix.Of]
// gives it, so attempts made before still count toward their client.
func TestClientPrefixMigration(t *testing.T) {
	// Given attempts saved by address before the migration.
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	_, err := provider.DownTo(t.Context(), beforeClientPrefixMigration)
	require.NoError(t, err)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	for _, addr := range savedAddrs {
		_, err = conn.Exec(t.Context(), "INSERT INTO sign_in_attempts (client_addr, created_at) VALUES ($1, now())", addr)
		require.NoError(t, err)
		_, err = conn.Exec(t.Context(), `INSERT INTO registration_attempts (email, client_addr, outcome, created_at)
			VALUES ('a@example.org', $1, 'link', now())`, addr)
		require.NoError(t, err)
	}

	// When the migration runs.
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	// Then each holds its prefix, written as Go writes it.
	var want []string
	for _, addr := range savedAddrs {
		want = append(want, clientprefix.Of(netip.MustParseAddr(addr)).String())
	}
	assert.Equal(t, want, queryStrings[string](t, conn, "SELECT client_prefix FROM sign_in_attempts ORDER BY id"))
	assert.Equal(t, want, queryStrings[string](t, conn, "SELECT client_prefix FROM registration_attempts ORDER BY id"))
}

// Down keeps each prefix's first address, which is the saved address for
// IPv4 and the start of the /64 for IPv6; Up applies again afterwards.
func TestClientPrefixMigrationDown(t *testing.T) {
	// Given attempts saved by prefix.
	url := newDatabase(t)
	provider := migratedProvider(t, url)
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer conn.Close(t.Context())
	for _, prefix := range []string{"192.0.2.1/32", "2001:db8:1:2::/64"} {
		_, err = conn.Exec(t.Context(), "INSERT INTO sign_in_attempts (client_prefix, created_at) VALUES ($1, now())", prefix)
		require.NoError(t, err)
	}

	// When the migration is undone.
	_, err = provider.DownTo(t.Context(), beforeClientPrefixMigration)
	require.NoError(t, err)

	// Then each holds an address again.
	assert.Equal(t, []string{"192.0.2.1", "2001:db8:1:2::"}, queryStrings[string](t, conn, "SELECT client_addr FROM sign_in_attempts ORDER BY id"))

	// And applying the migration again restores the prefixes.
	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.1/32", "2001:db8:1:2::/64"}, queryStrings[string](t, conn, "SELECT client_prefix FROM sign_in_attempts ORDER BY id"))
}
