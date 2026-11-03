package postgres

import (
	"crypto/sha256"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/emailverification"
	"inbok/internal/httpapi"
	"inbok/internal/passwordhash"
	"inbok/internal/session"
	"inbok/internal/totp"
)

const deactivatedPassword = "a long member password"

var deactivatedDevice = session.Device{Addr: netip.MustParseAddr("192.0.2.1")}

// deactivatedAccounts are a member who can sign in with deactivatedPassword,
// the administrator who manages them, and the services both use.
type deactivatedAccounts struct {
	db       *DB
	conn     *pgx.Conn
	sessions *session.Service
	members  *account.Members
	admin    int64
	member   int64
}

// newDeactivatedAccounts saves an administrator and an active member, with a
// confirmed factor when withTOTP is set, so their password sign-in asks for
// a code.
func newDeactivatedAccounts(t *testing.T, withTOTP bool) deactivatedAccounts {
	t.Helper()

	db, conn := setupStorage(t)
	accounts := account.NewService(db, "")
	f := deactivatedAccounts{
		db: db, conn: conn,
		sessions: session.NewService(db, accounts, session.NewThrottle(db, time.Now), time.Now),
		members:  account.NewMembers(db, time.Now),
	}
	require.NoError(t, conn.QueryRow(t.Context(),
		"INSERT INTO users (email, password_hash, role) VALUES ('admin@example.com', 'unused', 'admin') RETURNING id").Scan(&f.admin))
	require.NoError(t, conn.QueryRow(t.Context(),
		"INSERT INTO users (email, password_hash, role) VALUES ('member@example.com', $1, 'member') RETURNING id",
		hashPassword(t, deactivatedPassword)).Scan(&f.member))
	if withTOTP {
		_, err := conn.Exec(t.Context(),
			"INSERT INTO user_totp (user_id, key_id, secret_ciphertext, confirmed_at) VALUES ($1, '1', 'ciphertext', now())", f.member)
		require.NoError(t, err)
	}
	return f
}

// setDeactivated saves the member's flag directly, keeping their sessions
// and challenges, as if a request read them just before a deactivation.
func (f deactivatedAccounts) setDeactivated(t *testing.T, deactivated bool) {
	t.Helper()

	_, err := f.conn.Exec(t.Context(),
		"UPDATE users SET deactivated_at = CASE WHEN $2 THEN now() END WHERE id = $1", f.member, deactivated)
	require.NoError(t, err)
}

// A session that outlives its account's deactivation identifies no one, and
// works again once the account is reactivated.
func TestDeactivatedSessionCheck(t *testing.T) {
	// Given a signed-in member.
	f := newDeactivatedAccounts(t, false)
	signedIn, err := f.sessions.StartSignIn(t.Context(), "member@example.com", deactivatedPassword, deactivatedDevice)
	require.NoError(t, err)

	// When the session is checked while the member is deactivated, and again
	// after reactivation.
	f.setDeactivated(t, true)
	_, deactivatedErr := f.sessions.Current(t.Context(), signedIn.Secret)
	f.setDeactivated(t, false)
	current, reactivatedErr := f.sessions.Current(t.Context(), signedIn.Secret)

	// Then only the second check identifies the member.

	require.ErrorIs(t, deactivatedErr, session.ErrUnauthenticated)
	require.NoError(t, reactivatedErr)
	assert.Equal(t, f.member, current.Identity.User.ID)
}

// A deactivated member's wrong password fails as usual and counts toward the
// throttle; the right one says the account is deactivated and issues
// nothing.
func TestDeactivatedSignIn(t *testing.T) {
	for _, c := range []struct {
		name     string
		withTOTP bool
	}{
		{"session", false},
		{"challenge", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newDeactivatedAccounts(t, c.withTOTP)
			_, err := f.members.Deactivate(t.Context(), f.admin, f.member)
			require.NoError(t, err)

			_, wrongErr := f.sessions.StartSignIn(t.Context(), "member@example.com", "a wrong password", deactivatedDevice)
			_, rightErr := f.sessions.StartSignIn(t.Context(), "member@example.com", deactivatedPassword, deactivatedDevice)

			require.ErrorIs(t, wrongErr, account.ErrInvalidCredentials)
			require.ErrorIs(t, rightErr, account.ErrDeactivated)
			var attempts int
			require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT count(*) FROM sign_in_attempts").Scan(&attempts))
			assert.Equal(t, 1, attempts, "only the wrong password is a failure")
			assert.Zero(t, memberRowCount(t, f.conn, "sessions", f.member))
			assert.Zero(t, memberRowCount(t, f.conn, "sign_in_challenges", f.member))
		})
	}
}

// A reactivated member signs in as before, with a code if they have a
// factor.
func TestDeactivatedSignInReactivated(t *testing.T) {
	for _, c := range []struct {
		name     string
		withTOTP bool
		step     session.SignInStep
	}{
		{"session", false, session.SignInStepComplete},
		{"challenge", true, session.SignInStepTOTP},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newDeactivatedAccounts(t, c.withTOTP)
			_, err := f.members.Deactivate(t.Context(), f.admin, f.member)
			require.NoError(t, err)
			_, err = f.members.Reactivate(t.Context(), f.admin, f.member)
			require.NoError(t, err)

			signedIn, err := f.sessions.StartSignIn(t.Context(), "member@example.com", deactivatedPassword, deactivatedDevice)
			require.NoError(t, err)

			assert.Equal(t, c.step, signedIn.Step)
		})
	}
}

// A challenge that outlives its account's deactivation cannot complete: the
// code step deletes it and says the account is deactivated.
func TestDeactivatedCodeSignIn(t *testing.T) {
	f := newCodeFixture(t)
	challenge := startCodeSignIn(t, f)
	_, err := f.conn.Exec(t.Context(), "UPDATE users SET deactivated_at = now() WHERE id = $1", f.userID)
	require.NoError(t, err)

	issued, err := f.codes.Complete(t.Context(), challenge.Secret, f.code, deactivatedDevice)

	require.ErrorIs(t, err, account.ErrDeactivated)
	assert.Zero(t, issued)
	assert.Zero(t, memberRowCount(t, f.conn, "sign_in_challenges", f.userID))
	assert.Zero(t, memberRowCount(t, f.conn, "sessions", f.userID))
	assert.Zero(t, memberRowCount(t, f.conn, "totp_attempts", f.userID), "no code was checked, so the attempt is released")
}

// A request that passed the session check just before a deactivation
// changes nothing for the deactivated account: its TOTP and verification
// writes report the account missing, which handlers answer as signed out.
// The flag is set directly, keeping the session such a request checked.
func TestDeactivatedWrites(t *testing.T) {
	f := newDeactivatedAccounts(t, true)
	live := session.State{
		TokenHash: sha256.Sum256([]byte("live")), UserID: f.member,
		CreatedAt: time.Now(), LastUsedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, f.db.InsertSession(t.Context(), live))
	f.setDeactivated(t, true)
	before := memberSnapshot(t, f.conn, f.member)
	called := func(t *testing.T) { t.Error("read state for a deactivated account") }

	for _, c := range []struct {
		name  string
		write func(t *testing.T) error
		want  error
	}{
		{"start enrollment", func(t *testing.T) error {
			return f.db.UpdateTOTP(t.Context(), f.member, func(*totp.Factor) (totp.Factor, error) {
				called(t)
				return totp.Factor{}, nil
			})
		}, account.ErrUserNotFound},
		{"cancel enrollment", func(t *testing.T) error {
			return f.db.CancelTOTP(t.Context(), f.member, func(*totp.Factor) error {
				called(t)
				return nil
			})
		}, account.ErrUserNotFound},
		{"confirm enrollment", func(t *testing.T) error {
			return f.db.ConfirmTOTP(t.Context(), f.member, live.TokenHash, func(session.EnrollmentState) (session.EnrollmentChange, error) {
				called(t)
				return session.EnrollmentChange{}, nil
			})
		}, session.ErrNotFound},
		{"regenerate backup codes", func(t *testing.T) error {
			return f.db.ReplaceTOTPBackupCodes(t.Context(), f.member, func(*totp.Factor) (totp.BackupChange, error) {
				called(t)
				return totp.BackupChange{}, nil
			})
		}, account.ErrUserNotFound},
		{"turn off", func(t *testing.T) error {
			return f.db.DisableTOTP(t.Context(), f.member, 0, func(totp.DisableState) error {
				called(t)
				return nil
			})
		}, account.ErrUserNotFound},
		{"request verification link", func(t *testing.T) error {
			return f.db.RequestEmailVerification(t.Context(), f.member, func(emailverification.RequestState) (emailverification.RequestChange, error) {
				called(t)
				return emailverification.RequestChange{}, nil
			})
		}, account.ErrUserNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.ErrorIs(t, c.write(t), c.want)
		})
	}

	assert.Equal(t, before, memberSnapshot(t, f.conn, f.member))
}

// A verification link that somehow outlived its account's deactivation,
// which deletes links, verifies nothing.
func TestDeactivatedRedeem(t *testing.T) {
	f := newDeactivatedAccounts(t, false)
	link := verificationLink(f.member, "outlived", 0)
	link.Address, link.CreatedAt, link.ExpiresAt = "member@example.com", time.Now(), time.Now().Add(time.Hour)
	insertVerificationLink(t, f.conn, link)
	f.setDeactivated(t, true)
	before := memberSnapshot(t, f.conn, f.member)

	err := f.db.RedeemEmailVerification(t.Context(), link.TokenHash, func(emailverification.RedeemState) (emailverification.RedeemChange, error) {
		t.Error("redeem read state for a deactivated account")
		return emailverification.RedeemChange{}, nil
	})

	require.ErrorIs(t, err, emailverification.ErrLinkInvalid)
	assert.Equal(t, before, memberSnapshot(t, f.conn, f.member))
}

// Password sign-in racing a deactivation never leaves a session or a
// challenge behind: either the sign-in commits first and the deactivation
// deletes what it issued, or the sign-in sees the deactivation.
func TestDeactivatedSignInRace(t *testing.T) {
	const rounds = 15
	for _, c := range []struct {
		name     string
		withTOTP bool
	}{
		{"session", false},
		{"challenge", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newDeactivatedAccounts(t, c.withTOTP)
			outcomes := map[bool]int{} // whether the sign-in was refused
			// Sign-in spends most of its time hashing the password. Every
			// other round deactivates at once, which commits before the
			// sign-in takes the lock; the rest wait a random time of up to
			// twice the hash, landing before, during, or after it.
			credentials, err := f.db.ReadCredentials(t.Context(), "member@example.com")
			require.NoError(t, err)
			started := time.Now()
			_, err = passwordhash.Verify(t.Context(), deactivatedPassword, credentials.PasswordHash)
			require.NoError(t, err)
			window := 2 * time.Since(started)

			for round := range rounds {
				_, err := f.members.Reactivate(t.Context(), f.admin, f.member)
				require.NoError(t, err)
				var delay time.Duration
				if round%2 == 1 {
					delay = rand.N(window)
				}
				var wg sync.WaitGroup
				var signInErr, deactivateErr error
				wg.Go(func() {
					_, signInErr = f.sessions.StartSignIn(t.Context(), "member@example.com", deactivatedPassword, deactivatedDevice)
				})
				wg.Go(func() {
					time.Sleep(delay)
					_, deactivateErr = f.members.Deactivate(t.Context(), f.admin, f.member)
				})
				wg.Wait()

				require.NoError(t, deactivateErr)
				if signInErr != nil {
					require.ErrorIs(t, signInErr, account.ErrDeactivated)
				}
				outcomes[signInErr != nil]++
				assert.Zero(t, memberRowCount(t, f.conn, "sessions", f.member))
				assert.Zero(t, memberRowCount(t, f.conn, "sign_in_challenges", f.member))
			}
			t.Logf("sign-ins refused: %d, issued then ended: %d", outcomes[true], outcomes[false])
			assert.Positive(t, outcomes[true], "some sign-in must see the deactivation")
		})
	}
}

// deactivatedAPI gives f's administrator a confirmed factor and a session,
// and returns that session's cookie and a function that sends a same-origin
// request, with an optional JSON body and cookie, to an API using f's
// services.
func deactivatedAPI(t *testing.T, f deactivatedAccounts) (*http.Cookie, func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder) {
	t.Helper()

	_, err := f.conn.Exec(t.Context(),
		"INSERT INTO user_totp (user_id, key_id, secret_ciphertext, confirmed_at) VALUES ($1, '1', 'ciphertext', now())", f.admin)
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, f.db.InsertSession(t.Context(), session.State{
		TokenHash: sha256.Sum256(make([]byte, 32)), UserID: f.admin, CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(time.Hour),
	}))
	// This cookie is the base64url encoding of 32 zero bytes.
	admin := &http.Cookie{Name: "__Host-inbok-session", Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	api := httpapi.New(discard, httpapi.Services{Session: f.sessions, Members: f.members})
	send := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/api/v0"+path, strings.NewReader(body))
		request.Header.Set("Origin", "http://example.com")
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		return response
	}
	return admin, send
}

// Over HTTP, an administrator's deactivation signs the member out at once
// and refuses their sign-in.
func TestDeactivatedHTTP(t *testing.T) {
	// Given an administrator with admin access and a signed-in member.
	f := newDeactivatedAccounts(t, false)
	admin, send := deactivatedAPI(t, f)
	signIn := `{"email":"member@example.com","password":"` + deactivatedPassword + `"}`
	signedIn := send(http.MethodPost, "/sessions", signIn, nil)
	require.Equal(t, http.StatusCreated, signedIn.Code, signedIn.Body.String())
	cookies := signedIn.Result().Cookies()
	require.Len(t, cookies, 2)
	member := cookies[0]
	require.Equal(t, "__Host-inbok-session", member.Name)
	require.Equal(t, http.StatusOK, send(http.MethodGet, "/sessions/current", "", member).Code)
	memberPath := "/members/" + strconv.FormatInt(f.member, 10)

	// When the administrator deactivates the member, who then uses their
	// session and both a wrong and the right password.
	deactivated := send(http.MethodPost, memberPath+"/deactivate", "", admin)
	current := send(http.MethodGet, "/sessions/current", "", member)
	wrong := send(http.MethodPost, "/sessions", `{"email":"member@example.com","password":"a wrong password"}`, nil)
	refused := send(http.MethodPost, "/sessions", signIn, nil)

	// Then the session has ended and only the right password learns why.
	require.Equal(t, http.StatusOK, deactivated.Code, deactivated.Body.String())
	var body struct {
		Data struct {
			Member struct {
				ID            int64      `json:"id"`
				DeactivatedAt *time.Time `json:"deactivatedAt"`
			} `json:"member"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(deactivated.Body.Bytes(), &body))
	assert.Equal(t, f.member, body.Data.Member.ID)
	assert.NotNil(t, body.Data.Member.DeactivatedAt)
	assert.Equal(t, http.StatusUnauthorized, current.Code, current.Body.String())
	cleared := current.Result().Cookies()
	require.Len(t, cleared, 1)
	assert.Equal(t, "__Host-inbok-session", cleared[0].Name)
	assert.Negative(t, cleared[0].MaxAge)
	assert.Equal(t, http.StatusUnauthorized, wrong.Code, wrong.Body.String())
	assert.Contains(t, wrong.Body.String(), `"type":"/api/v0/problems/invalid-credentials"`)
	assert.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), `"type":"/api/v0/problems/account-deactivated"`)
}

// Over HTTP, a reactivated member signs in again.
func TestReactivatedHTTP(t *testing.T) {
	// Given a deactivated member and an administrator with admin access.
	f := newDeactivatedAccounts(t, false)
	_, err := f.members.Deactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)
	admin, send := deactivatedAPI(t, f)

	// When the administrator reactivates the member, who signs in.
	reactivated := send(http.MethodPost, "/members/"+strconv.FormatInt(f.member, 10)+"/reactivate", "", admin)
	signedIn := send(http.MethodPost, "/sessions", `{"email":"member@example.com","password":"`+deactivatedPassword+`"}`, nil)

	// Then the sign-in succeeds.
	require.Equal(t, http.StatusOK, reactivated.Code, reactivated.Body.String())
	assert.Contains(t, reactivated.Body.String(), `"deactivatedAt":null`)
	assert.Equal(t, http.StatusCreated, signedIn.Code, signedIn.Body.String())
}
