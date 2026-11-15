package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/attemptlimit"
	"inbok/internal/session"
	"inbok/internal/totp"
)

type fakeTOTP struct {
	status   totp.Status
	started  totp.Started
	codes    totp.Codes
	err      error
	calls    int
	user     account.User
	userID   int64
	password string
	code     string
	addr     netip.Addr
}

func (f *fakeTOTP) Status(_ context.Context, userID int64) (totp.Status, error) {
	f.calls++
	f.userID = userID
	return f.status, f.err
}

func (f *fakeTOTP) Start(_ context.Context, user account.User) (totp.Started, error) {
	f.calls++
	f.user = user
	return f.started, f.err
}

func (f *fakeTOTP) VerifyCode(_ context.Context, userID int64, code string) (totp.Codes, error) {
	f.calls++
	f.userID, f.code = userID, code
	return f.codes, f.err
}

func (f *fakeTOTP) ShowWaiting(_ context.Context, user account.User, password string, addr netip.Addr) (totp.Codes, error) {
	f.calls++
	f.user, f.password, f.addr = user, password, addr
	return f.codes, f.err
}

func (f *fakeTOTP) Cancel(_ context.Context, userID int64) error {
	f.calls++
	f.userID = userID
	return f.err
}

// enrollingSessions is an administrator without two-factor, enrolling.
func enrollingSessions() *fakeSessions {
	return memberSessions(account.RoleAdmin)
}

// throttledFor returns err as a throttle reports it, with a wait.
func throttledFor(err error) error {
	return &attemptlimit.Error{Err: err, Wait: time.Minute}
}

// assertFieldHoldsDetail asserts that body's one input error is on field
// and reads as the problem's detail, so a form that shows only the field
// loses nothing.
func assertFieldHoldsDetail(t *testing.T, body []byte, field string) {
	t.Helper()
	problem := decodeJSON(t, body)
	errs, ok := problem["errors"].([]any)
	require.True(t, ok, "no input errors")
	require.Len(t, errs, 1)
	input, ok := errs[0].(map[string]any)
	require.True(t, ok, "input error is not an object")
	assert.Equal(t, field, input["location"])
	assert.Equal(t, problem["detail"], input["message"])
}

// assertCodeFieldHoldsDetail is [assertFieldHoldsDetail] for body.code.
func assertCodeFieldHoldsDetail(t *testing.T, body []byte) {
	t.Helper()
	assertFieldHoldsDetail(t, body, "body.code")
}

type totpAuth int

const (
	totpAuthAnonymous totpAuth = iota
	totpAuthAuthenticated
)

func requestTOTP(api *API, method string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/v0/totp", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestStartTOTPEnrollment(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := &fakeTOTP{started: totp.Started{Secret: "manual-secret", URI: "otpauth://test"}}
			sessions := memberSessions(role)
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			api := New(logger, Services{TOTP: service, Session: sessions})
			response := requestTOTP(api, http.MethodPost, totpAuthAuthenticated)
			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"secret":"manual-secret","uri":"otpauth://test"}}`, response.Body.String())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Equal(t, sessions.identity.User, service.user)
			assert.Equal(t, 1, service.calls)
			assert.NotContains(t, logs.String(), service.started.Secret)
			assert.NotContains(t, logs.String(), service.started.URI)
		})
	}
}

func TestGetTOTPStatus(t *testing.T) {
	for _, status := range []totp.Status{
		{State: totp.StateOff},
		{State: totp.StatePending},
		{State: totp.StatePending, BackupCodesWaiting: true},
		{State: totp.StateExpired},
		{State: totp.StateOn, BackupCodesLeft: 7},
		{State: totp.StateOn, BackupCodesLeft: 10, BackupCodesWaiting: true},
	} {
		t.Run(fmt.Sprintf("%s %t", status.State, status.BackupCodesWaiting), func(t *testing.T) {
			service := &fakeTOTP{status: status}
			response := requestTOTP(New(discard, Services{TOTP: service, Session: enrollingSessions()}), http.MethodGet, totpAuthAuthenticated)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, fmt.Sprintf(`{"data":{"status":"%s","backupCodesLeft":%d,"backupCodesWaiting":%t}}`,
				status.State, status.BackupCodesLeft, status.BackupCodesWaiting), response.Body.String())
			assert.Equal(t, int64(42), service.userID)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

func TestTOTPRequiresSession(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			service := &fakeTOTP{}
			response := requestTOTP(New(discard, Services{TOTP: service}), method, totpAuthAnonymous)
			require.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Zero(t, service.calls)
			assert.NotContains(t, response.Body.String(), "secret")
		})
	}
}

func TestStartTOTPAlreadyOn(t *testing.T) {
	service := &fakeTOTP{err: totp.ErrAlreadyOn}
	response := requestTOTP(New(discard, Services{TOTP: service, Session: memberSessions(account.RoleMember)}), http.MethodPost, totpAuthAuthenticated)
	require.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/totp-already-on"`)
	assert.NotContains(t, response.Body.String(), `"secret"`)
}

// An account removed or deactivated after its session was checked is
// answered as signed out.
func TestStartTOTPAccountEnded(t *testing.T) {
	service := &fakeTOTP{err: fmt.Errorf("wrapped: %w", account.ErrUserNotFound)}

	response := requestTOTP(New(discard, Services{TOTP: service, Session: memberSessions(account.RoleMember)}), http.MethodPost, totpAuthAuthenticated)

	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/unauthenticated"`)
	assertSessionCookieCleared(t, response, true)
}

func TestTOTPUnexpectedFailure(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			service := &fakeTOTP{err: errors.New("private storage cause")}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			response := requestTOTP(New(logger, Services{TOTP: service, Session: memberSessions(account.RoleMember)}), method, totpAuthAuthenticated)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			assert.NotContains(t, response.Body.String(), "private storage cause")
			assert.Contains(t, logs.String(), "private storage cause")
		})
	}
}

func TestTOTPCrossOrigin(t *testing.T) {
	service := &fakeTOTP{}
	api := New(discard, Services{TOTP: service, Session: memberSessions(account.RoleMember)})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	assert.Zero(t, service.calls)
}

type fakeEnrollment struct {
	result  session.Confirmed
	issued  session.Finished
	err     error
	current session.Authenticated
	code    string
	token   string
	device  session.Device
	calls   int
}

func (f *fakeEnrollment) Confirm(_ context.Context, current session.Authenticated, code string, device session.Device) (session.Confirmed, error) {
	f.calls++
	f.current, f.code, f.device = current, code, device
	return f.result, f.err
}

func (f *fakeEnrollment) Finish(_ context.Context, current session.Authenticated, token string, device session.Device) (session.Finished, error) {
	f.calls++
	f.current, f.token, f.device = current, token, device
	return f.issued, f.err
}

func postTOTPConfirm(api *API, body, origin string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp/confirm", strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Origin", origin)
	request.Header.Set("User-Agent", "inbok-confirm-test/1.0")
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "old-cookie-secret"})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestConfirmTOTPEnrollment(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			sessions := memberSessions(role)
			sessions.reference = checkedReference(t)
			expires := time.Now().Add(2 * time.Hour)
			service := &fakeEnrollment{result: session.Confirmed{
				Session: session.Issued{Secret: "replacement-cookie-secret", State: session.State{ExpiresAt: expires}},
				BackupCodes: []string{
					"ABCDE-FGHJK", "MNPQR-STUVW", "XYZ23-45678", "9ABCD-EFGHJ", "KMNPQ-RSTUV",
					"WXYZ2-34567", "89ABC-DEFGH", "JKMNP-QRSTU", "VWXYZ-23456", "789AB-CDEFG",
				},
			}}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: sessions, Enrollment: service})

			response := postTOTPConfirm(api, `{"code":"287082"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body struct {
				Data totpConfirmation `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, service.result.BackupCodes, body.Data.BackupCodes)
			assert.Equal(t, session.Authenticated{Identity: sessions.identity, Session: sessions.reference}, service.current)
			assert.Equal(t, "287082", service.code)
			assert.Equal(t, session.Device{UserAgent: "inbok-confirm-test/1.0", Addr: netip.MustParseAddr("192.0.2.1")}, service.device)
			assert.Equal(t, 1, service.calls)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			cookies := response.Result().Cookies()
			require.Len(t, cookies, 1)
			cookie := cookies[0]
			assert.Equal(t, sessionCookieName, cookie.Name)
			assert.Equal(t, service.result.Session.Secret, cookie.Value)
			assert.Equal(t, "/", cookie.Path)
			assert.Empty(t, cookie.Domain)
			assert.True(t, cookie.Secure)
			assert.True(t, cookie.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			assert.InDelta(t, int((2*time.Hour)/time.Second), cookie.MaxAge, 2)
			for _, secret := range []string{"287082", "old-cookie-secret", service.result.Session.Secret, "ABCDE-FGHJK", "MNPQR-STUVW"} {
				assert.NotContains(t, logs.String(), secret)
			}
			assert.NotContains(t, response.Body.String(), service.result.Session.Secret)
		})
	}
}

func TestConfirmTOTPFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"invalid code", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-totp-code"},
		{"not pending", totp.ErrNotPending, http.StatusConflict, "totp-not-pending"},
		{"already on", totp.ErrAlreadyOn, http.StatusConflict, "totp-already-on"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"session revoked during confirmation", session.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
		{"commit failed", errors.New("private commit failure"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeEnrollment{err: fmt.Errorf("confirmation: %w", c.err), result: session.Confirmed{
				Session: session.Issued{Secret: "must-not-issue"}, BackupCodes: []string{"must-not-reveal"},
			}}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: memberSessions(account.RoleMember), Enrollment: service})

			response := postTOTPConfirm(api, `{"code":"287082"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assertSessionCookieCleared(t, response, c.problem == "unauthenticated")
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			for _, secret := range []string{"287082", "must-not-issue", "must-not-reveal"} {
				assert.NotContains(t, response.Body.String(), secret)
				assert.NotContains(t, logs.String(), secret)
			}
			assert.NotContains(t, response.Body.String(), "private commit failure")
			if c.err == totp.ErrInvalidCode {
				assertCodeFieldHoldsDetail(t, response.Body.Bytes())
			}
		})
	}
}

func TestConfirmTOTPRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"code":"287082"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing body", ``, "http://example.com", totpAuthAuthenticated, http.StatusBadRequest},
		{"missing code", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"wrong type", `{"code":287082}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"too long", `{"code":"1234567"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeEnrollment{}
			api := New(discard, Services{Session: memberSessions(account.RoleMember), Enrollment: service})

			response := postTOTPConfirm(api, c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
			assert.NotContains(t, response.Body.String(), "287082")
		})
	}
}

func TestConfirmTOTPOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/totp/confirm"]
	require.NotNil(t, path)
	op := path.Post
	require.NotNil(t, op)
	assert.Equal(t, "confirmTOTPEnrollment", op.OperationID)
	assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		require.NotNil(t, op.Responses[strconv.Itoa(status)])
	}
	require.NotNil(t, op.Responses["200"].Headers["Set-Cookie"])
	input := doc.Components.Schemas.Map()["TotpConfirmationCode"]
	require.NotNil(t, input)
	require.NotNil(t, input.Properties["code"])
	assert.True(t, input.Properties["code"].WriteOnly)
	result := doc.Components.Schemas.Map()["TotpConfirmation"]
	require.NotNil(t, result)
	codes := result.Properties["backupCodes"]
	require.NotNil(t, codes)
	assert.False(t, codes.Nullable)
	require.NotNil(t, codes.MinItems)
	require.NotNil(t, codes.MaxItems)
	assert.Equal(t, 10, *codes.MinItems)
	assert.Equal(t, 10, *codes.MaxItems)
}

// ========================
// Enrollment code check
// ========================

func TestCheckTOTPEnrollmentCode(t *testing.T) {
	service := &fakeTOTP{codes: totp.Codes{Codes: tenCodes, Token: "one-time-token"}}
	var logs bytes.Buffer
	api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{TOTP: service, Session: enrollingSessions()})

	response := postTOTP(api, "/enrollment/code", `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data waitingBackupCodes `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, tenCodes, body.Data.BackupCodes)
	assert.Equal(t, "one-time-token", body.Data.Token)
	assert.Equal(t, int64(42), service.userID)
	assert.Equal(t, "012345", service.code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Empty(t, response.Result().Cookies(), "two-factor is still off; the session stays")
	for _, secret := range append([]string{"012345", "one-time-token", "session-secret"}, tenCodes...) {
		assert.NotContains(t, logs.String(), secret)
	}
}

func TestCheckTOTPCodeProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"invalid code", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-totp-code"},
		{"not pending", totp.ErrNotPending, http.StatusConflict, "totp-not-pending"},
		{"expired", totp.ErrEnrollmentExpired, http.StatusConflict, "totp-enrollment-expired"},
		{"code checked", totp.ErrCodeChecked, http.StatusConflict, "totp-code-checked"},
		{"already on", totp.ErrAlreadyOn, http.StatusConflict, "totp-already-on"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"account removed", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated"},
		{"commit failed", errors.New("private commit failure"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeTOTP{err: fmt.Errorf("check: %w", c.err), codes: totp.Codes{Codes: []string{"must-not-reveal"}}}
			api := New(discard, Services{TOTP: service, Session: enrollingSessions()})

			response := postTOTP(api, "/enrollment/code", `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assertSessionCookieCleared(t, response, c.problem == "unauthenticated")
			for _, secret := range []string{"012345", "must-not-reveal", "private commit failure"} {
				assert.NotContains(t, response.Body.String(), secret)
			}
			if c.problem == "invalid-totp-code" {
				assertCodeFieldHoldsDetail(t, response.Body.Bytes())
			}
		})
	}
}

func TestCheckTOTPCodeRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"code":"287082"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing body", ``, "http://example.com", totpAuthAuthenticated, http.StatusBadRequest},
		{"missing code", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"wrong type", `{"code":287082}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"too long", `{"code":"1234567"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeTOTP{}
			api := New(discard, Services{TOTP: service, Session: enrollingSessions()})

			response := postTOTP(api, "/enrollment/code", c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
			assert.NotContains(t, response.Body.String(), "287082")
		})
	}
}

// ========================
// Enrollment finish
// ========================

func TestFinishTOTPEnrollment(t *testing.T) {
	sessions := enrollingSessions()
	sessions.reference = checkedReference(t)
	expires := time.Now().Add(2 * time.Hour)
	service := &fakeEnrollment{issued: session.Finished{
		Issued:          session.Issued{Secret: "replacement-cookie-secret", State: session.State{ExpiresAt: expires}},
		BackupCodesLeft: 10,
	}}
	var logs bytes.Buffer
	api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: sessions, Enrollment: service})

	response := postTOTP(api, "/enrollment/finish", `{"token":"one-time-token"}`, "http://example.com", totpAuthAuthenticated)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"status":"on","backupCodesLeft":10,"backupCodesWaiting":false}}`, response.Body.String())
	assert.Equal(t, session.Authenticated{Identity: sessions.identity, Session: sessions.reference}, service.current)
	assert.Equal(t, "one-time-token", service.token)
	assert.Equal(t, session.Device{UserAgent: "inbok-totp-test/1.0", Addr: netip.MustParseAddr("192.0.2.1")}, service.device)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, sessionCookieName, cookie.Name)
	assert.Equal(t, service.issued.Secret, cookie.Value)
	assert.Equal(t, "/", cookie.Path)
	assert.Empty(t, cookie.Domain)
	assert.True(t, cookie.Secure)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.InDelta(t, int((2*time.Hour)/time.Second), cookie.MaxAge, 2)
	for _, secret := range []string{"one-time-token", "session-secret", service.issued.Secret} {
		assert.NotContains(t, logs.String(), secret)
	}
	assert.NotContains(t, response.Body.String(), service.issued.Secret)
}

func TestFinishTOTPProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"not pending", totp.ErrNotPending, http.StatusConflict, "totp-not-pending"},
		{"expired", totp.ErrEnrollmentExpired, http.StatusConflict, "totp-enrollment-expired"},
		{"not waiting", totp.ErrNotWaiting, http.StatusConflict, "backup-codes-not-waiting"},
		{"already on", totp.ErrAlreadyOn, http.StatusConflict, "totp-already-on"},
		{"session revoked while finishing", session.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
		{"commit failed", errors.New("private commit failure"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeEnrollment{err: fmt.Errorf("finish: %w", c.err), issued: session.Finished{Issued: session.Issued{Secret: "must-not-issue"}}}
			api := New(discard, Services{Session: enrollingSessions(), Enrollment: service})

			response := postTOTP(api, "/enrollment/finish", `{"token":"one-time-token"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assertSessionCookieCleared(t, response, c.problem == "unauthenticated")
			for _, secret := range []string{"one-time-token", "must-not-issue", "private commit failure"} {
				assert.NotContains(t, response.Body.String(), secret)
			}
		})
	}
}

func TestFinishTOTPRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"token":"t"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing token", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"too long", `{"token":"` + strings.Repeat("t", 65) + `"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeEnrollment{}
			api := New(discard, Services{Session: enrollingSessions(), Enrollment: service})

			response := postTOTP(api, "/enrollment/finish", c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

// ========================
// Waiting backup codes
// ========================

func TestShowWaitingBackupCodes(t *testing.T) {
	service := &fakeTOTP{codes: totp.Codes{Codes: tenCodes, Token: "fresh-token"}}
	var logs bytes.Buffer
	api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{TOTP: service, Session: enrollingSessions()})

	response := postTOTP(api, "/backup-codes/waiting", `{"password":"current password"}`, "http://example.com", totpAuthAuthenticated)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data waitingBackupCodes `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, tenCodes, body.Data.BackupCodes)
	assert.Equal(t, "fresh-token", body.Data.Token)
	assert.Equal(t, "current password", service.password)
	assert.Equal(t, netip.MustParseAddr("192.0.2.1"), service.addr)
	assert.Empty(t, response.Result().Cookies())
	for _, secret := range append([]string{"current password", "fresh-token"}, tenCodes...) {
		assert.NotContains(t, logs.String(), secret)
	}
}

func TestShowWaitingBackupCodesProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"not waiting", totp.ErrNotWaiting, http.StatusConflict, "backup-codes-not-waiting"},
		{"expired", totp.ErrEnrollmentExpired, http.StatusConflict, "totp-enrollment-expired"},
		{"two-factor on", totp.ErrAlreadyOn, http.StatusConflict, "totp-already-on"},
		{"wrong password", account.ErrInvalidCredentials, http.StatusUnprocessableEntity, "invalid-password"},
		{"throttled", throttledFor(session.ErrThrottled), http.StatusTooManyRequests, "sign-in-throttled"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeTOTP{err: c.err, codes: totp.Codes{Codes: []string{"must-not-reveal"}}}
			api := New(discard, Services{TOTP: service, Session: enrollingSessions()})

			response := postTOTP(api, "/backup-codes/waiting", `{"password":"pw"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assert.NotContains(t, response.Body.String(), "must-not-reveal")
		})
	}
}

func TestTOTPEnrollmentStepsOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	for _, c := range []struct {
		path, id   string
		statuses   []int
		setsCookie bool
	}{
		{"/totp/enrollment/code", "checkTOTPEnrollmentCode", []int{200, 401, 409, 422, 429}, false},
		{"/totp/enrollment/finish", "finishTOTPEnrollment", []int{200, 401, 409}, true},
		{"/totp/backup-codes/waiting", "showWaitingTOTPBackupCodes", []int{200, 401, 409, 422, 429}, false},
	} {
		t.Run(c.id, func(t *testing.T) {
			path := doc.Paths[c.path]
			require.NotNil(t, path)
			op := path.Post
			require.NotNil(t, op)
			assert.Equal(t, c.id, op.OperationID)
			assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
			assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
			for _, status := range c.statuses {
				require.NotNil(t, op.Responses[strconv.Itoa(status)], "status %d", status)
			}
			assert.Equal(t, c.setsCookie, op.Responses["200"].Headers["Set-Cookie"] != nil)
		})
	}

	schemas := doc.Components.Schemas.Map()
	for name, field := range map[string]string{"PasswordProof": "password", "TotpEnrollmentCode": "code"} {
		input := schemas[name]
		require.NotNil(t, input, name)
		require.NotNil(t, input.Properties[field], name)
		assert.True(t, input.Properties[field].WriteOnly, name)
	}
}

// ========================
// Backup-code replacement
// ========================

// tenCodes is a backup-code set a fake returns.
var tenCodes = []string{
	"ABCDE-FGHJK", "MNPQR-STUVW", "XYZ23-45678", "9ABCD-EFGHJ", "KMNPQ-RSTUV",
	"WXYZ2-34567", "89ABC-DEFGH", "JKMNP-QRSTU", "VWXYZ-23456", "789AB-CDEFG",
}

// postTOTP sends body as JSON to the two-factor operation at path, below
// /api/v0/totp.
func postTOTP(api *API, path, body, origin string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Origin", origin)
	request.Header.Set("User-Agent", "inbok-totp-test/1.0")
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

// confirmedSessions is an account with two-factor on.
func confirmedSessions(role account.Role) *fakeSessions {
	sessions := memberSessions(role)
	if role == account.RoleAdmin {
		sessions.identity.Access, sessions.identity.TOTPRequired = session.AccessAdmin, false
	}
	return sessions
}

type fakeBackups struct {
	codes  totp.Codes
	active int
	err    error
	calls  int
	userID int64
	code   string
	token  string
}

func (f *fakeBackups) Replace(_ context.Context, userID int64, code string) (totp.Codes, error) {
	f.calls++
	f.userID, f.code = userID, code
	return f.codes, f.err
}

func (f *fakeBackups) Finish(_ context.Context, userID int64, token string) (int, error) {
	f.calls++
	f.userID, f.token = userID, token
	return f.active, f.err
}

func TestReplaceBackupCodes(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := &fakeBackups{codes: totp.Codes{Codes: tenCodes, Token: "one-time-token"}}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: confirmedSessions(role), Backups: service})

			response := postTOTP(api, "/backup-codes", `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body struct {
				Data waitingBackupCodes `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, tenCodes, body.Data.BackupCodes)
			assert.Equal(t, "one-time-token", body.Data.Token)
			assert.Equal(t, int64(42), service.userID)
			assert.Equal(t, "012345", service.code)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies(), "replacement does not replace or clear sessions")
			for _, secret := range append([]string{"012345", "one-time-token", "session-secret"}, tenCodes...) {
				assert.NotContains(t, logs.String(), secret)
			}
		})
	}
}

func TestReplaceBackupCodesProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"not on", totp.ErrNotOn, http.StatusConflict, "totp-not-on"},
		{"invalid code", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-totp-code"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"deleted user", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated"},
		{"commit failure", errors.New("private storage cause"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeBackups{codes: totp.Codes{Codes: []string{"must-not-reveal"}}, err: fmt.Errorf("replace: %w", c.err)}
			api := New(discard, Services{Session: memberSessions(account.RoleMember), Backups: service})

			response := postTOTP(api, "/backup-codes", `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assertSessionCookieCleared(t, response, c.problem == "unauthenticated")
			for _, secret := range []string{"012345", "must-not-reveal", "private storage cause"} {
				assert.NotContains(t, response.Body.String(), secret)
			}
			if c.err == totp.ErrInvalidCode {
				assertCodeFieldHoldsDetail(t, response.Body.Bytes())
			}
		})
	}
}

func TestReplaceBackupCodesRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"code":"012345"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing code", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"wrong type", `{"code":123456}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"backup code", `{"code":"ABCDE-FGHJK"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeBackups{}
			api := New(discard, Services{Session: memberSessions(account.RoleMember), Backups: service})

			response := postTOTP(api, "/backup-codes", c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestFinishBackupReplacement(t *testing.T) {
	service := &fakeBackups{active: 10}
	api := New(discard, Services{Session: memberSessions(account.RoleMember), Backups: service})

	response := postTOTP(api, "/backup-codes/finish", `{"token":"one-time-token"}`, "http://example.com", totpAuthAuthenticated)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"status":"on","backupCodesLeft":10,"backupCodesWaiting":false}}`, response.Body.String())
	assert.Equal(t, int64(42), service.userID)
	assert.Equal(t, "one-time-token", service.token)
	assert.Empty(t, response.Result().Cookies())
}

func TestFinishBackupReplacementProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"not waiting", totp.ErrNotWaiting, http.StatusConflict, "backup-codes-not-waiting"},
		{"not on", totp.ErrNotOn, http.StatusConflict, "totp-not-on"},
		{"deleted user", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeBackups{err: c.err}
			api := New(discard, Services{Session: memberSessions(account.RoleMember), Backups: service})

			response := postTOTP(api, "/backup-codes/finish", `{"token":"one-time-token"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
		})
	}
}

func TestBackupReplacementOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	for _, c := range []struct {
		path, id string
		statuses []int
	}{
		{"/totp/backup-codes", "replaceTOTPBackupCodes", []int{200, 401, 409, 422, 429}},
		{"/totp/backup-codes/finish", "finishTOTPBackupCodeReplacement", []int{200, 401, 409}},
	} {
		t.Run(c.id, func(t *testing.T) {
			path := doc.Paths[c.path]
			require.NotNil(t, path)
			op := path.Post
			require.NotNil(t, op)
			assert.Equal(t, c.id, op.OperationID)
			assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
			assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
			for _, status := range c.statuses {
				require.NotNil(t, op.Responses[strconv.Itoa(status)], "status %d", status)
			}
		})
	}

	schemas := doc.Components.Schemas.Map()
	for name, field := range map[string]string{"TotpReplacementCode": "code", "BackupCodesToken": "token"} {
		input := schemas[name]
		require.NotNil(t, input, name)
		require.NotNil(t, input.Properties[field], name)
		assert.True(t, input.Properties[field].WriteOnly, name)
	}
	codes := schemas["WaitingBackupCodes"].Properties["backupCodes"]
	require.NotNil(t, codes)
	assert.False(t, codes.Nullable)
	require.NotNil(t, codes.MinItems)
	require.NotNil(t, codes.MaxItems)
	assert.Equal(t, 10, *codes.MinItems)
	assert.Equal(t, 10, *codes.MaxItems)
}

// ========================
// TOTP turn-off
// ========================

type fakeDisabler struct {
	err    error
	calls  int
	userID int64
	code   string
}

func (f *fakeDisabler) Disable(_ context.Context, userID int64, code string) error {
	f.calls++
	f.userID, f.code = userID, code
	return f.err
}

func postDisableTOTP(api *API, body, origin string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/totp/disable", strings.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("Origin", origin)
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	}

	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestDisableTOTP(t *testing.T) {
	for _, code := range []string{"012345", "abcde-fghjk"} {
		t.Run(code, func(t *testing.T) {
			service := &fakeDisabler{}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Session: memberSessions(account.RoleMember), Disabler: service})

			response := postDisableTOTP(api, `{"code":"`+code+`"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"status":"off","backupCodesLeft":0,"backupCodesWaiting":false}}`, response.Body.String())
			assert.Equal(t, int64(42), service.userID)
			assert.Equal(t, code, service.code)
			assert.Equal(t, 1, service.calls)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies())
			assert.NotContains(t, response.Body.String(), code)
			assert.NotContains(t, logs.String(), code)
			assert.NotContains(t, logs.String(), "session-secret")
		})
	}
}

func TestDisableTOTPProblems(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"admin", totp.ErrDisableForbidden, http.StatusForbidden, "totp-disable-forbidden"},
		{"not on", totp.ErrNotOn, http.StatusConflict, "totp-not-on"},
		{"invalid proof", totp.ErrInvalidCode, http.StatusUnprocessableEntity, "invalid-totp-proof"},
		{"throttled", totp.ErrThrottled, http.StatusTooManyRequests, "totp-throttled"},
		{"deleted user", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated"},
		{"commit failure", errors.New("private turn-off cause"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeDisabler{err: fmt.Errorf("turn off: %w", c.err)}
			sessions := memberSessions(account.RoleMember)
			if c.err == totp.ErrDisableForbidden {
				sessions = memberSessions(account.RoleAdmin)
				sessions.identity.Access = session.AccessAdmin
				sessions.identity.TOTPRequired = false
			}
			api := New(discard, Services{Session: sessions, Disabler: service})

			response := postDisableTOTP(api, `{"code":"012345"}`, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assert.Equal(t, 1, service.calls, "the service owns the locked role check")
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assertSessionCookieCleared(t, response, c.problem == "unauthenticated")
			assert.NotContains(t, response.Body.String(), "private turn-off cause")
			assert.NotContains(t, response.Body.String(), "012345")
			assert.NotContains(t, response.Body.String(), `"status":"off"`)
			if c.err == totp.ErrInvalidCode {
				assertCodeFieldHoldsDetail(t, response.Body.Bytes())
			}
		})
	}
}

func TestDisableTOTPRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, body, origin string
		auth               totpAuth
		status             int
	}{
		{"anonymous before parsing", `{`, "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", `{"code":"012345"}`, "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing code", `{}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"wrong type", `{"code":123456}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
		{"oversized", `{"code":"` + strings.Repeat("A", 129) + `"}`, "http://example.com", totpAuthAuthenticated, http.StatusUnprocessableEntity},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeDisabler{}
			api := New(discard, Services{Session: memberSessions(account.RoleMember), Disabler: service})

			response := postDisableTOTP(api, c.body, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestDisableTOTPOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/totp/disable"]
	require.NotNil(t, path)
	op := path.Post
	require.NotNil(t, op)
	assert.Equal(t, "disableTOTP", op.OperationID)
	assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		require.NotNil(t, op.Responses[strconv.Itoa(status)])
	}
	input := doc.Components.Schemas.Map()["TotpDisableCode"]
	require.NotNil(t, input)
	require.NotNil(t, input.Properties["code"])
	assert.True(t, input.Properties["code"].WriteOnly)
	assert.Nil(t, op.Responses["200"].Headers["Set-Cookie"])
}

func deleteTOTPEnrollment(api *API, origin string, auth totpAuth) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodDelete, "/api/v0/totp/enrollment", nil)
	request.Header.Set("Origin", origin)
	if auth == totpAuthAuthenticated {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-session-secret"})
	}
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func TestCancelTOTPEnrollment(t *testing.T) {
	for _, role := range []account.Role{account.RoleMember, account.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			service := &fakeTOTP{}
			var logs bytes.Buffer
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{TOTP: service, Session: memberSessions(role)})

			response := deleteTOTPEnrollment(api, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, `{"data":{"status":"off","backupCodesLeft":0,"backupCodesWaiting":false}}`, response.Body.String())
			assert.Equal(t, int64(42), service.userID, "use the checked account")
			assert.Equal(t, 1, service.calls, "success must not perform another status read")
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assert.Empty(t, response.Result().Cookies())
			assert.NotContains(t, logs.String(), "private-session-secret")
		})
	}
}

func TestCancelTOTPFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		status  int
		problem string
	}{
		{"confirmed", totp.ErrAlreadyOn, http.StatusConflict, "totp-already-on"},
		{"wrapped confirmed", fmt.Errorf("wrapped: %w", totp.ErrAlreadyOn), http.StatusConflict, "totp-already-on"},
		{"removed account", account.ErrUserNotFound, http.StatusUnauthorized, "unauthenticated"},
		{"wrapped removed account", fmt.Errorf("wrapped: %w", account.ErrUserNotFound), http.StatusUnauthorized, "unauthenticated"},
		{"storage failure", errors.New("private cancellation cause"), http.StatusInternalServerError, "internal-error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeTOTP{err: c.err}
			api := New(discard, Services{TOTP: service, Session: memberSessions(account.RoleMember)})

			response := deleteTOTPEnrollment(api, "http://example.com", totpAuthAuthenticated)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"type":"/api/v0/problems/`+c.problem+`"`)
			assert.Equal(t, 1, service.calls)
			assert.NotContains(t, response.Body.String(), "private cancellation cause")
			assert.NotContains(t, response.Body.String(), `"status":"off"`)
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			assertSessionCookieCleared(t, response, c.problem == "unauthenticated")
		})
	}
}

func TestCancelTOTPRequestGate(t *testing.T) {
	for _, c := range []struct {
		name, origin string
		auth         totpAuth
		status       int
	}{
		{"anonymous", "http://example.com", totpAuthAnonymous, http.StatusUnauthorized},
		{"cross origin", "https://untrusted.example", totpAuthAuthenticated, http.StatusForbidden},
		{"missing source", "", totpAuthAuthenticated, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &fakeTOTP{}
			api := New(discard, Services{TOTP: service, Session: memberSessions(account.RoleMember)})

			response := deleteTOTPEnrollment(api, c.origin, c.auth)

			require.Equal(t, c.status, response.Code, response.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestCancelTOTPOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/totp/enrollment"]
	require.NotNil(t, path)
	op := path.Delete
	require.NotNil(t, op)
	assert.Equal(t, "cancelTOTPEnrollment", op.OperationID)
	assert.Equal(t, session.AccessMember, op.Metadata[accessKey])
	assert.Equal(t, []map[string][]string{{sessionSecuritySchemeName: {}}}, op.Security)
	assert.Nil(t, op.RequestBody, "cancellation needs no code or account selector")
	assert.Empty(t, op.Parameters)
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusConflict} {
		require.NotNil(t, op.Responses[strconv.Itoa(status)])
	}
	assert.Nil(t, op.Responses["200"].Headers["Set-Cookie"])
}
