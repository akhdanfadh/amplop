package postgres

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/hotp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/encryption"
	"inbok/internal/httpapi"
	"inbok/internal/passwordhash"
	"inbok/internal/repository/postgres/sqlc"
	"inbok/internal/session"
	"inbok/internal/totp"
)

const codeSignInPassword = "a long sign-in password"

type codeFixture struct {
	db       *DB
	conn     *pgx.Conn
	sessions *session.Service
	codes    *session.CodeSignIn
	api      *httpapi.API
	userID   int64
	now      time.Time
	code     string
}

func newCodeFixture(t *testing.T) *codeFixture {
	t.Helper()
	db, conn := setupStorage(t)
	accounts := account.NewService(db, setupTestToken)
	user, err := accounts.Setup(t.Context(), account.SetupInput{Token: setupTestToken, Email: "alice@example.com", Password: codeSignInPassword})
	require.NoError(t, err)
	f := &codeFixture{db: db, conn: conn, userID: user.ID, now: time.Now().UTC().Truncate(time.Microsecond)}
	clock := func() time.Time { return f.now }
	ring, err := encryption.New("1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	require.NoError(t, err)
	id, ciphertext, err := ring.Seal([]byte(confirmationSecret), "totp:"+strconv.FormatInt(user.ID, 10))
	require.NoError(t, err)
	require.NoError(t, db.UpdateTOTP(t.Context(), user.ID, func(*totp.Factor) (totp.Factor, error) {
		return totp.Factor{KeyID: id, Ciphertext: ciphertext, ConfirmedAt: &f.now, LastStep: -1}, nil
	}))
	require.NoError(t, sqlc.New(conn).InsertTOTPBackupCode(t.Context(), sqlc.InsertTOTPBackupCodeParams{UserID: user.ID, CodeHash: passwordhash.Hash("ABCDEFGHJK")}))
	f.code, err = hotp.GenerateCode(confirmationSecret, uint64(f.now.Unix()/30))
	require.NoError(t, err)
	logger := slog.New(slog.DiscardHandler)
	f.sessions = session.NewService(db, accounts, session.NewThrottle(db, clock), clock)
	f.codes = session.NewCodeSignIn(db, ring, totp.NewVerifier(clock), totp.NewThrottle(db, clock, logger), clock)
	f.api = httpapi.New(logger, httpapi.Services{Session: f.sessions, CodeSignIn: f.codes})
	return f
}

func startCodeSignIn(t *testing.T, f *codeFixture) session.SignedIn {
	t.Helper()
	result, err := f.sessions.StartSignIn(t.Context(), "alice@example.com", codeSignInPassword, netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)
	require.Equal(t, session.SignInStepTOTP, result.Step)
	return result
}

func codePost(f *codeFixture, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	f.api.Handler().ServeHTTP(response, request)
	return response
}

func TestCodeSignInHTTP(t *testing.T) {
	f := newCodeFixture(t)

	password := codePost(f, "/sessions", `{"email":"alice@example.com","password":"a long sign-in password"}`, nil)

	require.Equal(t, http.StatusCreated, password.Code, password.Body.String())
	assert.JSONEq(t, `{"data":{"step":"totp"}}`, password.Body.String())
	assert.Equal(t, "no-store", password.Header().Get("Cache-Control"))
	cookies := password.Result().Cookies()
	require.Len(t, cookies, 2)
	challenge := cookies[0]
	assert.Equal(t, "__Host-inbok-challenge", challenge.Name)
	assert.Positive(t, challenge.MaxAge)
	assert.True(t, challenge.Secure)
	assert.True(t, challenge.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, challenge.SameSite)
	assert.Negative(t, cookies[1].MaxAge)
	_, err := f.sessions.Current(t.Context(), challenge.Value)
	require.ErrorIs(t, err, session.ErrUnauthenticated, "a challenge cannot authorize a session")

	wrong := codePost(f, "/sessions/totp", `{"code":"wrong"}`, challenge)
	require.Equal(t, http.StatusUnprocessableEntity, wrong.Code, wrong.Body.String())
	assert.Contains(t, wrong.Body.String(), `"location":"body.code"`)
	assert.Empty(t, wrong.Result().Cookies())
	completed := codePost(f, "/sessions/totp", `{"code":"`+f.code+`"}`, challenge)
	require.Equal(t, http.StatusCreated, completed.Code, completed.Body.String())
	assert.JSONEq(t, `{"data":{"step":"complete"}}`, completed.Body.String())
	cookies = completed.Result().Cookies()
	require.Len(t, cookies, 2)
	assert.Equal(t, "__Host-inbok-session", cookies[0].Name)
	assert.Negative(t, cookies[1].MaxAge)
	current, err := f.sessions.Current(t.Context(), cookies[0].Value)
	require.NoError(t, err)
	assert.Equal(t, f.userID, current.Identity.User.ID)
	assert.Equal(t, session.AccessAdmin, current.Identity.Access)
	assert.False(t, current.Identity.TOTPRequired)
	assert.NotContains(t, completed.Body.String(), cookies[0].Value)

	reused := codePost(f, "/sessions/totp", `{"code":"`+f.code+`"}`, challenge)
	require.Equal(t, http.StatusUnauthorized, reused.Code, reused.Body.String())
	cookies = reused.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "__Host-inbok-challenge", cookies[0].Name)
	assert.Negative(t, cookies[0].MaxAge)
	var attempts int
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_attempts WHERE user_id=$1", f.userID).Scan(&attempts))
	assert.Equal(t, 1, attempts, "success removes only its own reservation")
}

func TestCodeFailureLimit(t *testing.T) {
	f := newCodeFixture(t)
	challenge := startCodeSignIn(t, f)
	for i := 1; i <= 5; i++ {
		_, err := f.codes.Complete(t.Context(), challenge.Secret, "wrong")
		if i == 5 {
			require.ErrorIs(t, err, session.ErrInvalidChallenge)
			continue
		}
		require.ErrorIs(t, err, totp.ErrInvalidCode)
	}
	var count int
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_challenges").Scan(&count))
	assert.Zero(t, count)
	newChallenge := startCodeSignIn(t, f)
	_, err := f.codes.Complete(t.Context(), newChallenge.Secret, f.code)
	require.ErrorIs(t, err, totp.ErrThrottled, "a fresh challenge must not reset user limits")
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&count))
	assert.Zero(t, count)
}

func TestCodeChallengeExpiry(t *testing.T) {
	f := newCodeFixture(t)
	challenge := startCodeSignIn(t, f)
	f.now = challenge.ExpiresAt

	issued, err := f.codes.Complete(t.Context(), challenge.Secret, f.code)

	require.ErrorIs(t, err, session.ErrInvalidChallenge)
	assert.Empty(t, issued)
	var count int
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_challenges").Scan(&count))
	assert.Zero(t, count)
}

func TestCodeSignInParallel(t *testing.T) {
	for _, kind := range []string{"totp", "backup"} {
		t.Run(kind, func(t *testing.T) {
			f := newCodeFixture(t)
			first, second := startCodeSignIn(t, f), startCodeSignIn(t, f)
			code := f.code
			if kind == "backup" {
				code = "abcde-fghjk"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			start, results := make(chan struct{}), make(chan error, 2)
			for _, secret := range []string{first.Secret, second.Secret} {
				go func() { <-start; _, err := f.codes.Complete(ctx, secret, code); results <- err }()
			}
			close(start)
			var accepted, rejected int
			for range 2 {
				err := <-results
				if errors.Is(err, totp.ErrInvalidCode) {
					rejected++
					continue
				}
				require.NoError(t, err)
				accepted++
			}
			assert.Equal(t, 1, accepted)
			assert.Equal(t, 1, rejected)
			var count int
			require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&count))
			assert.Equal(t, 1, count)
			if kind == "backup" {
				require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM totp_backup_codes").Scan(&count))
				assert.Zero(t, count)
			}
		})
	}
}

func TestCodeSignInRollback(t *testing.T) {
	for _, kind := range []string{"totp", "backup"} {
		t.Run(kind, func(t *testing.T) {
			f := newCodeFixture(t)
			challenge := startCodeSignIn(t, f)
			_, err := f.conn.Exec(t.Context(), `CREATE FUNCTION reject_code_signin() RETURNS trigger LANGUAGE plpgsql AS
			$$ BEGIN RAISE EXCEPTION 'forced sign-in failure'; END $$;
			CREATE CONSTRAINT TRIGGER reject_code_signin AFTER INSERT ON sessions
			DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_code_signin();`)
			require.NoError(t, err)
			code := f.code
			if kind == "backup" {
				code = "ABCDE-FGHJK"
			}

			issued, err := f.codes.Complete(t.Context(), challenge.Secret, code)

			require.ErrorContains(t, err, "forced sign-in failure")
			assert.Empty(t, issued)
			var challenges, codes, sessions, attempts int
			var step int64
			require.NoError(t, f.conn.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sign_in_challenges),
			(SELECT count(*) FROM totp_backup_codes), (SELECT count(*) FROM sessions),
			(SELECT count(*) FROM totp_attempts), (SELECT last_step FROM user_totp WHERE user_id=$1)`, f.userID).Scan(&challenges, &codes, &sessions, &attempts, &step))
			assert.Equal(t, 1, challenges)
			assert.Equal(t, 1, codes)
			assert.Zero(t, sessions)
			assert.Equal(t, 1, attempts)
			assert.Equal(t, int64(-1), step)
		})
	}
}

func TestPasswordEnrollmentRace(t *testing.T) {
	f := newCodeFixture(t)
	_, err := f.conn.Exec(t.Context(), "UPDATE user_totp SET confirmed_at=NULL WHERE user_id=$1", f.userID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.conn.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", f.userID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "UPDATE user_totp SET confirmed_at=$1 WHERE user_id=$2", f.now, f.userID)
	require.NoError(t, err)
	type result struct {
		issued session.SignedIn
		err    error
	}
	done := make(chan result, 1)
	go func() {
		issued, err := f.sessions.StartSignIn(ctx, "alice@example.com", codeSignInPassword, netip.MustParseAddr("192.0.2.1"))
		done <- result{issued, err}
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		err := f.db.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockTOTPUser%')").Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))
	completed := <-done
	require.NoError(t, completed.err)
	assert.Equal(t, session.SignInStepTOTP, completed.issued.Step, "read the confirmed factor after acquiring the owner lock")
}
