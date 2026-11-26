package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/emailaddress"
	"amplop/internal/session"
)

// ========================
// Test helpers
// ========================

const (
	setupHTTPToken  = "test-only-setup-token-for-http-tests"
	jsonContentType = "application/json"
)

func setupBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	return string(data)
}

func setupFields() map[string]any {
	return map[string]any{"token": setupHTTPToken, "email": "Admin@example.com", "password": "a long setup password"}
}

type fakeAccounts struct {
	status   account.SetupStatus
	err      error
	setupErr error
	created  []account.SetupInput
}

func (f *fakeAccounts) SetupStatus(context.Context) (account.SetupStatus, error) {
	return f.status, f.err
}

func (f *fakeAccounts) Setup(_ context.Context, input account.SetupInput) (account.User, error) {
	f.created = append(f.created, input)
	return account.User{ID: 7, Email: "admin@example.com"}, f.setupErr
}

func postSetup(logger *slog.Logger, accounts *fakeAccounts, body string) *httptest.ResponseRecorder {
	return postSetupSignIn(logger, accounts, &fakeFirstAdmin{}, body)
}

func postSetupSignIn(logger *slog.Logger, accounts *fakeAccounts, signIn *fakeFirstAdmin, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "amplop-setup-test/1.0")
	response := httptest.NewRecorder()
	New(logger, Services{Account: accounts, Throttle: &fakeThrottle{}, FirstAdminSignIn: signIn}).Handler().ServeHTTP(response, request)
	return response
}

// fakeFirstAdmin signs the new administrator in with a fixed secret.
type fakeFirstAdmin struct {
	err    error
	calls  int
	userID int64
	device session.Device
}

func (f *fakeFirstAdmin) SignInFirstAdmin(_ context.Context, userID int64, device session.Device) (session.SignedIn, error) {
	f.calls++
	f.userID, f.device = userID, device
	if f.err != nil {
		return session.SignedIn{}, f.err
	}
	return session.SignedIn{Step: session.SignInStepComplete, Secret: "first-admin-session", ExpiresAt: time.Now().Add(15 * time.Minute)}, nil
}

// ========================
// Setup behavior
// ========================

func TestGetSetupStatus(t *testing.T) {
	for _, status := range []account.SetupStatus{account.SetupUnconfigured, account.SetupOpen, account.SetupDone} {
		t.Run(string(status), func(t *testing.T) {
			accounts := &fakeAccounts{status: status}
			router := New(discard, Services{Account: accounts, Throttle: &fakeThrottle{}}).Handler()
			response := httptest.NewRecorder()

			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/setup", nil))

			require.Equal(t, http.StatusOK, response.Code)
			assert.JSONEq(t, `{"data":{"status":"`+string(status)+`"}}`, response.Body.String())
			assert.Empty(t, response.Header().Values("Set-Cookie"))
		})
	}
}

func TestCreateFirstAdmin(t *testing.T) {
	accounts := &fakeAccounts{}
	signIn := &fakeFirstAdmin{}
	body := `{"token":"test-only-setup-token-for-http-tests","email":"Admin@Example.COM","password":"a long setup password"}`

	response := postSetupSignIn(discard, accounts, signIn, body)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"id":7,"email":"admin@example.com"}}`, response.Body.String())
	assert.Equal(t, []account.SetupInput{{
		Token: "test-only-setup-token-for-http-tests", Email: "Admin@Example.COM", Password: "a long setup password",
	}}, accounts.created)

	// Setup signs the new administrator in, with enrollment access only.
	assert.Equal(t, 1, signIn.calls)
	assert.Equal(t, int64(7), signIn.userID)
	assert.Equal(t, session.Device{UserAgent: "amplop-setup-test/1.0", Addr: netip.MustParseAddr("192.0.2.1")}, signIn.device)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 2)
	assert.Equal(t, sessionCookieName, cookies[0].Name)
	assert.Equal(t, "first-admin-session", cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)
	assert.True(t, cookies[0].Secure)
	assert.InDelta(t, int((15*time.Minute)/time.Second), cookies[0].MaxAge, 2)
	assert.Equal(t, challengeCookieName, cookies[1].Name)
	assert.Negative(t, cookies[1].MaxAge)
}

// When signing in fails after setup commits, the administrator exists and
// signs in as usual; the request reports the failure without a cookie.
func TestCreateFirstAdminSignInFailure(t *testing.T) {
	accounts := &fakeAccounts{}
	signIn := &fakeFirstAdmin{err: errors.New("private session failure")}

	response := postSetupSignIn(discard, accounts, signIn, setupBody(t, setupFields()))

	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	assert.Len(t, accounts.created, 1)
	assert.Empty(t, response.Result().Cookies())
	assert.NotContains(t, response.Body.String(), "private session failure")
}

// A refused setup signs no one in.
func TestCreateFirstAdminRefusedNoSignIn(t *testing.T) {
	accounts := &fakeAccounts{setupErr: account.ErrSetupDone}
	signIn := &fakeFirstAdmin{}

	response := postSetupSignIn(discard, accounts, signIn, setupBody(t, setupFields()))

	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	assert.Zero(t, signIn.calls)
	assert.Empty(t, response.Result().Cookies())
}

// An email the service rejects is the same field error as one Huma rejects.
func TestSetupRejectedEmail(t *testing.T) {
	accounts := &fakeAccounts{setupErr: emailaddress.ErrInvalid}

	response := postSetup(discard, accounts, setupBody(t, setupFields()))

	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, problemPrefix+"validation-error", body["type"])
	assert.Equal(t, []any{map[string]any{"location": "body.email", "message": "Enter an email address, like name@example.com."}}, body["errors"])
}

func TestSetupProblems(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		typ  string
	}{
		{"done", account.ErrSetupDone, http.StatusConflict, "/api/v0/problems/setup-done"},
		{"unconfigured", account.ErrSetupUnconfigured, http.StatusConflict, "/api/v0/problems/setup-unconfigured"},
		{"wrong token", account.ErrSetupTokenInvalid, http.StatusForbidden, "/api/v0/problems/setup-token-invalid"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			accounts := &fakeAccounts{setupErr: c.err}
			fields := setupFields()
			var logs bytes.Buffer

			response := postSetup(slog.New(slog.NewTextHandler(&logs, nil)), accounts, setupBody(t, fields))

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, c.typ, body["type"])
			assert.Nil(t, body["errors"])
			require.Len(t, accounts.created, 1)
			for _, secret := range []string{setupHTTPToken, fields["email"].(string), fields["password"].(string)} {
				assert.NotContains(t, response.Body.String(), secret)
				assert.NotContains(t, logs.String(), secret)
			}
		})
	}
}

func TestSetupClaimDone(t *testing.T) {
	accounts := &fakeAccounts{setupErr: account.ErrSetupDone}

	response := postSetup(discard, accounts, setupBody(t, setupFields()))

	require.Equal(t, http.StatusConflict, response.Code)
	body := decodeJSON(t, response.Body.Bytes())
	assert.Equal(t, "/api/v0/problems/setup-done", body["type"])
}

func TestSetupUnexpectedError(t *testing.T) {
	for _, stage := range []string{"status", "claim"} {
		t.Run(stage, func(t *testing.T) {
			lost := errors.New("database connection lost")
			accounts := &fakeAccounts{}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			var response *httptest.ResponseRecorder
			switch stage {
			case "status":
				accounts.err = lost
				response = httptest.NewRecorder()
				New(logger, Services{Account: accounts, Throttle: &fakeThrottle{}}).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/setup", nil))
			case "claim":
				accounts.setupErr = lost
				response = postSetup(logger, accounts, setupBody(t, setupFields()))
			}

			require.Equal(t, http.StatusInternalServerError, response.Code)
			assert.NotContains(t, response.Body.String(), lost.Error())
			assert.Contains(t, logs.String(), lost.Error())
			assert.NotContains(t, logs.String(), setupHTTPToken)
			assert.NotContains(t, logs.String(), "a long setup password")
		})
	}
}

// ========================
// Input validation
// ========================

func TestSetupFieldsFirst(t *testing.T) {
	for _, err := range []error{account.ErrSetupDone, account.ErrSetupUnconfigured, account.ErrSetupTokenInvalid} {
		t.Run(err.Error(), func(t *testing.T) {
			accounts := &fakeAccounts{setupErr: err}
			fields := setupFields()
			fields["password"] = "private-short"

			response := postSetup(discard, accounts, setupBody(t, fields))

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), "body.password")
			assert.NotContains(t, response.Body.String(), "private-short")
			assert.Empty(t, accounts.created)
		})
	}
}

func TestSetupValidation(t *testing.T) {
	longest := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	cases := []struct {
		name, field, message string
		value                any
	}{
		{"bad email", "email", "Enter an email address, like name@example.com.", "rejected-email-secret"},
		{"display name", "email", "Enter an email address, like name@example.com.", "Admin <admin@example.com>"},
		{"long email", "email", "Use 3 to 254 characters.", "a" + longest},
		{"email type", "email", "Enter an email address, like name@example.com.", 7},
		{"null email", "email", "Enter an email address, like name@example.com.", nil},
		{"short password", "password", "Use 15 to 128 characters.", strings.Repeat("p", 14)},
		{"long password", "password", "Use 15 to 128 characters.", strings.Repeat("p", 129)},
		{"short Unicode password", "password", "Use 15 to 128 characters.", strings.Repeat("\U0001f512", 14)},
		{"password type", "password", "Enter a valid value.", 7},
		{"null password", "password", "Enter a valid value.", nil},
		{"extra field", "extra", "Enter a valid value.", "rejected-extra-secret"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			accounts := &fakeAccounts{}
			fields := setupFields()
			fields[c.field] = c.value

			response := postSetup(discard, accounts, setupBody(t, fields))

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			body := decodeJSON(t, response.Body.Bytes())
			assert.Equal(t, "/api/v0/problems/validation-error", body["type"])
			details, _ := body["errors"].([]any)
			require.Len(t, details, 1)
			detail, _ := details[0].(map[string]any)
			assert.Equal(t, "body."+c.field, detail["location"])
			assert.Equal(t, c.message, detail["message"])
			assert.NotContains(t, detail, "value")
			assert.NotContains(t, response.Body.String(), setupHTTPToken)
			assert.Empty(t, accounts.created)
		})
	}
}

func TestSetupMissingFields(t *testing.T) {
	for _, field := range []string{"token", "email", "password"} {
		t.Run(field, func(t *testing.T) {
			accounts := &fakeAccounts{}
			fields := setupFields()
			delete(fields, field)

			response := postSetup(discard, accounts, setupBody(t, fields))

			require.Equal(t, http.StatusUnprocessableEntity, response.Code)
			body := decodeJSON(t, response.Body.Bytes())
			details, _ := body["errors"].([]any)
			require.Len(t, details, 1)
			detail, _ := details[0].(map[string]any)
			assert.Equal(t, "body."+field, detail["location"])
			assert.Equal(t, "This field is required.", detail["message"])
			assert.Empty(t, accounts.created)
		})
	}
}

func TestSetupPasswordBounds(t *testing.T) {
	for _, password := range []string{strings.Repeat("p", 15), strings.Repeat("p", 128), strings.Repeat("\U0001f512", 15), strings.Repeat("\U0001f512", 128), strings.Repeat(" ", 15)} {
		accounts := &fakeAccounts{}
		fields := setupFields()
		fields["password"] = password

		response := postSetup(discard, accounts, setupBody(t, fields))

		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		require.Len(t, accounts.created, 1)
		assert.Equal(t, password, accounts.created[0].Password)
	}
}

func TestSetupFieldAliases(t *testing.T) {
	accounts := &fakeAccounts{}
	body := `{"Token":"test-only-setup-token-for-http-tests","Email":"Admin@example.com","Password":"a long setup password"}`

	response := postSetup(discard, accounts, body)

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Len(t, accounts.created, 1)
	assert.Equal(t, setupHTTPToken, accounts.created[0].Token)
	assert.Equal(t, "a long setup password", accounts.created[0].Password)
}

func TestSetupDuplicateFields(t *testing.T) {
	for _, field := range []string{"token", "email", "password"} {
		t.Run(field, func(t *testing.T) {
			accounts := &fakeAccounts{}
			body := setupFields()
			body[strings.ToUpper(field)] = "private-duplicate"

			response := postSetup(discard, accounts, setupBody(t, body))

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			assert.Empty(t, accounts.created)
			assert.NotContains(t, response.Body.String(), "private-duplicate")
		})
	}
}

func TestSetupBodyErrors(t *testing.T) {
	cases := []struct {
		name, body, contentType string
		code                    int
	}{
		{"missing body", "", "application/json", http.StatusBadRequest},
		{"malformed JSON", `{"token":"private-token",`, "application/json", http.StatusBadRequest},
		{"unsupported type", `{"token":"private-token"}`, "text/plain", http.StatusUnsupportedMediaType},
		{"timeout", "", "application/json", http.StatusRequestTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			accounts := &fakeAccounts{}
			request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(c.body))
			request.Header.Set("Origin", "http://"+request.Host)
			request.Header.Set("Content-Type", c.contentType)
			if c.name == "timeout" {
				request.Body = timedOutBody{}
			}
			response := httptest.NewRecorder()

			New(discard, Services{Account: accounts, Throttle: &fakeThrottle{}}).Handler().ServeHTTP(response, request)

			require.Equal(t, c.code, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "private-token")
			assert.Empty(t, accounts.created)
		})
	}
}

func TestSetupBodyShape(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"token":7,"email":false,"password":null}`} {
		accounts := &fakeAccounts{setupErr: account.ErrSetupDone}

		response := postSetup(discard, accounts, body)

		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		assert.Empty(t, accounts.created)
	}
}

func TestSetupBodyLimit(t *testing.T) {
	accounts := &fakeAccounts{}
	api := New(discard, Services{Account: accounts, Throttle: &fakeThrottle{}})
	limit := api.api.OpenAPI().Paths[setupPath].Post.MaxBodyBytes
	require.Positive(t, limit)
	request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(strings.Repeat("x", int(limit)+1)))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Content-Type", jsonContentType)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.Empty(t, accounts.created)
}

// Requests share the registered API while Huma validates each body separately.
func TestSetupConcurrentValidation(t *testing.T) {
	const requests = 20
	accounts := &fakeAccounts{}
	router := New(discard, Services{Account: accounts, Throttle: &fakeThrottle{}}).Handler()
	body := setupFields()
	body["password"] = "private-short"
	encoded := setupBody(t, body)

	var group sync.WaitGroup
	for range requests {
		group.Go(func() {
			request := httptest.NewRequest(http.MethodPost, "/api/v0/setup", strings.NewReader(encoded))
			request.Header.Set("Origin", "http://"+request.Host)
			request.Header.Set("Content-Type", jsonContentType)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
			assert.Contains(t, response.Body.String(), "Use 15 to 128 characters.")
			assert.NotContains(t, response.Body.String(), "private-short")
		})
	}
	group.Wait()
	assert.Empty(t, accounts.created)
}

// ========================
// OpenAPI contract
// ========================

func TestSetupOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	path := doc.Paths["/setup"]
	require.NotNil(t, path)
	require.NotNil(t, path.Get)
	require.NotNil(t, path.Post)
	assert.Equal(t, "getSetupStatus", path.Get.OperationID)
	assert.Equal(t, "createFirstAdmin", path.Post.OperationID)
	require.NotNil(t, path.Post.RequestBody)
	assert.True(t, path.Post.RequestBody.Required)

	media := path.Post.RequestBody.Content[jsonContentType]
	require.NotNil(t, media)
	ref := media.Schema
	require.NotNil(t, ref)
	require.Equal(t, "#/components/schemas/NewSetup", ref.Ref)
	schema := doc.Components.Schemas.SchemaFromRef(ref.Ref)
	require.NotNil(t, schema)
	assert.ElementsMatch(t, []string{"token", "email", "password"}, schema.Required)
	for _, name := range []string{"token", "email", "password"} {
		require.NotNil(t, schema.Properties[name], name)
	}
	assert.True(t, schema.Properties["token"].WriteOnly)
	assert.True(t, schema.Properties["password"].WriteOnly)
	assert.Equal(t, "idn-email", schema.Properties["email"].Format)
	for name, limits := range map[string][2]int{"email": {3, 254}, "password": {15, 128}} {
		field := schema.Properties[name]
		require.NotNil(t, field.MinLength, name)
		require.NotNil(t, field.MaxLength, name)
		assert.Equal(t, limits[0], *field.MinLength)
		assert.Equal(t, limits[1], *field.MaxLength)
	}

	require.Len(t, path.Get.Responses, 2)
	require.NotNil(t, path.Get.Responses[strconv.Itoa(http.StatusOK)])
	require.NotNil(t, path.Get.Responses["default"])
	require.Len(t, path.Post.Responses, 5)
	for _, code := range []int{http.StatusCreated, http.StatusForbidden, http.StatusConflict, http.StatusTooManyRequests} {
		require.NotNil(t, path.Post.Responses[strconv.Itoa(code)])
	}
	fallback := path.Post.Responses["default"]
	require.NotNil(t, fallback)
	require.NotNil(t, fallback.Content[problemContentType])
	assert.Equal(t, "#/components/schemas/Problem", fallback.Content[problemContentType].Schema.Ref)
	assert.Nil(t, fallback.Content[problemContentType].Example)
	assert.Empty(t, fallback.Content[problemContentType].Examples)

	forbidden := path.Post.Responses[strconv.Itoa(http.StatusForbidden)].Content[problemContentType]
	require.NotNil(t, forbidden)
	invalid, ok := forbidden.Example.(*problem)
	require.True(t, ok)
	assert.Equal(t, setupTokenInvalidType, invalid.Type)
	assert.Empty(t, forbidden.Examples)

	conflict := path.Post.Responses[strconv.Itoa(http.StatusConflict)].Content[problemContentType]
	require.NotNil(t, conflict)
	for name, typ := range map[string]string{"setupDone": setupDoneType, "setupUnconfigured": setupUnconfiguredType} {
		example := conflict.Examples[name]
		require.NotNil(t, example, name)
		p, ok := example.Value.(*problem)
		require.True(t, ok)
		assert.Equal(t, typ, p.Type)
	}
	status := doc.Components.Schemas.Map()["SetupStatus"]
	require.NotNil(t, status)
	statusSchema := status.Properties["status"]
	require.NotNil(t, statusSchema)
	assert.ElementsMatch(t, []any{"unconfigured", "open", "done"}, statusSchema.Enum)
}

// Keep the HTTP contract's literal tags tied to the password rule used by recovery.
func TestSetupPasswordRule(t *testing.T) {
	field, ok := reflect.TypeFor[newSetup]().FieldByName("Password")
	require.True(t, ok)
	assert.Equal(t, strconv.Itoa(account.PasswordMinLength), field.Tag.Get("minLength"))
	assert.Equal(t, strconv.Itoa(account.PasswordMaxLength), field.Tag.Get("maxLength"))
}
