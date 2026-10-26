package postgres

import (
	"context"
	"crypto/rand"
	"log"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// testImage matches compose.yaml: tests run the PostgreSQL that
// development does. Change both together.
const testImage = "postgres:18"

// adminURL points at the container's default database. Tests use it only
// to create and drop their own databases.
var adminURL string

// TestMain starts one PostgreSQL container for the package. Docker must be
// running; without it the tests fail rather than skip.
func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, testImage, tcpostgres.BasicWaitStrategies())
	if err != nil {
		log.Fatalf("start PostgreSQL (is Docker running?): %v", err)
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("stop PostgreSQL: %v", err)
		}
	}()

	adminURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("PostgreSQL URL: %v", err)
	}

	m.Run()
}

// newDatabase creates an empty database for one test and drops it after,
// so tests never see each other's rows. It returns the database's URL.
func newDatabase(t *testing.T) string {
	t.Helper()

	name := "test_" + strings.ToLower(rand.Text())
	adminExec(t, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())

	// FORCE closes connections the test left open.
	t.Cleanup(func() { adminExec(t, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)") })

	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// adminExec runs one statement on the container's default database.
func adminExec(t *testing.T, sql string) {
	t.Helper()

	// Not t.Context(): it is already cancelled when cleanups run.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, sql)
	require.NoError(t, err)
}
