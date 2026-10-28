package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

type fakeThrottle struct {
	beginErr  error
	deleteErr error
	addr      netip.Addr
	begun     int
	removed   []int64
}

func (f *fakeThrottle) BeginSetup(_ context.Context, addr netip.Addr) (int64, error) {
	f.begun++
	f.addr = addr
	return 42, f.beginErr
}

func (f *fakeThrottle) Succeed(_ context.Context, id int64) error {
	f.removed = append(f.removed, id)
	return f.deleteErr
}

func TestSetupThrottling(t *testing.T) {
	lost := errors.New("attempt storage unavailable")
	for _, c := range []struct {
		name      string
		beginErr  error
		setupErr  error
		deleteErr error
		code      int
		created   int
		removed   []int64
	}{
		{"success", nil, nil, nil, http.StatusCreated, 1, []int64{42}},
		{"invalid token", nil, account.ErrSetupTokenInvalid, nil, http.StatusForbidden, 1, nil},
		{"complete", nil, account.ErrSetupDone, nil, http.StatusConflict, 1, nil},
		{"unconfigured", nil, account.ErrSetupUnconfigured, nil, http.StatusConflict, 1, nil},
		{"throttled", session.ErrThrottled, nil, nil, http.StatusTooManyRequests, 0, nil},
		{"reserve failure", lost, nil, nil, http.StatusInternalServerError, 0, nil},
		{"setup storage failure", nil, lost, nil, http.StatusInternalServerError, 1, nil},
		{"remove failure", nil, nil, lost, http.StatusInternalServerError, 1, []int64{42}},
	} {
		t.Run(c.name, func(t *testing.T) {
			accounts := &fakeAccounts{setupErr: c.setupErr}
			limiter := &fakeThrottle{beginErr: c.beginErr, deleteErr: c.deleteErr}
			api := New(discard, Services{Account: accounts, Throttle: limiter})
			require.NoError(t, api.SetTrustedProxies([]string{"10.0.0.0/8"}))
			request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(setupBody(t, setupFields())))
			request.Header.Set("Origin", "http://"+request.Host)
			request.Header.Set("Content-Type", jsonContentType)
			request.Header.Set("X-Forwarded-For", "198.51.100.20")
			request.RemoteAddr = "10.1.2.3:1234"
			response := httptest.NewRecorder()

			api.Handler().ServeHTTP(response, request)

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, netip.MustParseAddr("198.51.100.20"), limiter.addr)
			assert.Len(t, accounts.created, c.created)
			assert.Equal(t, c.removed, limiter.removed)
			assert.Empty(t, response.Result().Cookies())
			if c.beginErr == session.ErrThrottled {
				assert.Equal(t, "/api/v0/problems/sign-in-throttled", decodeJSON(t, response.Body.Bytes())["type"])
			}
		})
	}
}

func TestSetupValidationFirst(t *testing.T) {
	limiter := &fakeThrottle{beginErr: session.ErrThrottled}
	accounts := &fakeAccounts{}
	api := New(discard, Services{Account: accounts, Throttle: limiter})
	request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(`{"token":"private-token","email":"bad","password":"short"}`))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)
	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Zero(t, limiter.begun)
	assert.Empty(t, accounts.created)
}
