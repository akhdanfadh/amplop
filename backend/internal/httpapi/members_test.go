package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/session"
)

var (
	membersJoined      = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	membersDeactivated = time.Date(2026, 11, 2, 9, 30, 0, 123456000, time.UTC)
)

// fakeMembers records what the member operations ask of the service.
type fakeMembers struct {
	members []account.Member
	member  account.Member
	err     error

	calls    int
	actorID  int64
	memberID int64
	action   string // "deactivate" or "reactivate"
}

func (f *fakeMembers) List(context.Context) ([]account.Member, error) {
	f.calls++
	return f.members, f.err
}

func (f *fakeMembers) Deactivate(_ context.Context, actorID, memberID int64) (account.Member, error) {
	f.calls++
	f.actorID, f.memberID, f.action = actorID, memberID, "deactivate"
	return f.member, f.err
}

func (f *fakeMembers) Reactivate(_ context.Context, actorID, memberID int64) (account.Member, error) {
	f.calls++
	f.actorID, f.memberID, f.action = actorID, memberID, "reactivate"
	return f.member, f.err
}

// deactivatedMember is a verified member with a confirmed factor,
// deactivated at [membersDeactivated].
func deactivatedMember() account.Member {
	deactivated := membersDeactivated
	return account.Member{
		User:     account.User{ID: 7, Email: "member@example.com", Role: account.RoleMember},
		JoinedAt: membersJoined, EmailVerified: true, TOTPEnabled: true, DeactivatedAt: &deactivated,
	}
}

const deactivatedMemberJSON = `{"id":7,"email":"member@example.com","role":"member","joinedAt":"2026-03-01T09:00:00Z",` +
	`"emailVerified":true,"totpEnabled":true,"deactivatedAt":"2026-11-02T09:30:00.123456Z",` +
	`"allowedActions":["deactivate","reactivate"]}`

func TestListMembers(t *testing.T) {
	service := &fakeMembers{members: []account.Member{
		{User: account.User{ID: 42, Email: "admin@example.com", Role: account.RoleAdmin}, JoinedAt: membersJoined.Add(-time.Hour)},
		deactivatedMember(),
	}}
	api := New(discard, Services{Members: service, Session: adminSessions()})

	response := requestAuthenticated(api, http.MethodGet, "/members", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"members":[`+
		`{"id":42,"email":"admin@example.com","role":"admin","joinedAt":"2026-03-01T08:00:00Z",`+
		`"emailVerified":false,"totpEnabled":false,"deactivatedAt":null,"allowedActions":[]},`+
		deactivatedMemberJSON+`]}}`, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

// An instance always has its administrator, but an empty list is still a
// list, not null.
func TestListMembersEmpty(t *testing.T) {
	api := New(discard, Services{Members: &fakeMembers{}, Session: adminSessions()})

	response := requestAuthenticated(api, http.MethodGet, "/members", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"members":[]}}`, response.Body.String())
}

// Each change acts on the member the path names, on behalf of the
// signed-in administrator, and returns the member as saved.
func TestChangeMember(t *testing.T) {
	for _, action := range []string{"deactivate", "reactivate"} {
		t.Run(action, func(t *testing.T) {
			service := &fakeMembers{member: deactivatedMember()}
			api := New(discard, Services{Members: service, Session: adminSessions()})

			response := requestAuthenticated(api, http.MethodPost, "/members/7/"+action, "")

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Equal(t, action, service.action)
			assert.Equal(t, int64(42), service.actorID, "the actor is the signed-in administrator")
			assert.Equal(t, int64(7), service.memberID)
			assert.JSONEq(t, `{"data":{"member":`+deactivatedMemberJSON+`}}`, response.Body.String())
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

// A refused change has its own problem type, so the web can tell an unknown
// account from an administrator.
func TestChangeMemberRefused(t *testing.T) {
	for _, action := range []string{"deactivate", "reactivate"} {
		for _, c := range []struct {
			name   string
			err    error
			status int
			typ    string
		}{
			{"unknown", account.ErrUserNotFound, http.StatusNotFound, "member-not-found"},
			{"administrator", fmt.Errorf("wrapped: %w", account.ErrAdminTarget), http.StatusConflict, "member-is-administrator"},
		} {
			t.Run(action+" "+c.name, func(t *testing.T) {
				api := New(discard, Services{Members: &fakeMembers{err: c.err}, Session: adminSessions()})

				response := requestAuthenticated(api, http.MethodPost, "/members/7/"+action, "")

				require.Equal(t, c.status, response.Code, response.Body.String())
				assert.Equal(t, problemPrefix+c.typ, decodeJSON(t, response.Body.Bytes())["type"])
			})
		}
	}
}

// A path ID that is not a positive whole number is rejected before the
// service runs.
func TestChangeMemberInvalidID(t *testing.T) {
	for _, id := range []string{"0", "-1", "seven", "7.5", "9223372036854775808"} {
		t.Run(id, func(t *testing.T) {
			service := &fakeMembers{}
			api := New(discard, Services{Members: service, Session: adminSessions()})

			response := requestAuthenticated(api, http.MethodPost, "/members/"+id+"/deactivate", "")

			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			errs, ok := decodeJSON(t, response.Body.Bytes())["errors"].([]any)
			require.True(t, ok, response.Body.String())
			require.Len(t, errs, 1)
			field := errs[0].(map[string]any)
			assert.Equal(t, "path.id", field["location"])
			assert.NotContains(t, field, "value", "the rejected value is not echoed")
			assert.Zero(t, service.calls)
		})
	}
}

// An unexpected failure is a fixed 500 whose cause stays in the log.
func TestMembersUnexpectedFailure(t *testing.T) {
	for _, op := range []struct{ name, method, path string }{
		{"list", http.MethodGet, "/members"},
		{"deactivate", http.MethodPost, "/members/7/deactivate"},
		{"reactivate", http.MethodPost, "/members/7/reactivate"},
	} {
		t.Run(op.name, func(t *testing.T) {
			var logs bytes.Buffer
			service := &fakeMembers{err: errors.New("private storage cause")}
			api := New(slog.New(slog.NewTextHandler(&logs, nil)), Services{Members: service, Session: adminSessions()})

			response := requestAuthenticated(api, op.method, op.path, "")

			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "private storage cause")
			assert.Contains(t, logs.String(), "private storage cause")
		})
	}
}

// Only an administrator with two-factor authentication reaches the service.
func TestMembersAccess(t *testing.T) {
	member := &fakeSessions{identity: session.Identity{User: account.User{ID: 7, Role: account.RoleMember}, Access: session.AccessMember}}
	pending := &fakeSessions{identity: session.Identity{User: account.User{ID: 42, Role: account.RoleAdmin}, Access: session.AccessEnrollment, TOTPRequired: true}}
	for _, op := range []struct{ name, method, path string }{
		{"list", http.MethodGet, "/members"},
		{"deactivate", http.MethodPost, "/members/7/deactivate"},
		{"reactivate", http.MethodPost, "/members/7/reactivate"},
	} {
		for _, c := range []struct {
			name     string
			sessions *fakeSessions
			code     int
			typ      string
		}{
			{"member", member, http.StatusForbidden, "forbidden"},
			{"admin without TOTP", pending, http.StatusForbidden, "totp-required"},
			{"signed out", &fakeSessions{currentErr: session.ErrUnauthenticated}, http.StatusUnauthorized, "unauthenticated"},
		} {
			t.Run(op.name+" "+c.name, func(t *testing.T) {
				service := &fakeMembers{}
				api := New(discard, Services{Members: service, Session: c.sessions})

				response := requestAuthenticated(api, op.method, op.path, "")

				require.Equal(t, c.code, response.Code, response.Body.String())
				assert.Equal(t, problemPrefix+c.typ, decodeJSON(t, response.Body.Bytes())["type"])
				assert.Zero(t, service.calls)
			})
		}
	}
}

func TestMembersOpenAPI(t *testing.T) {
	doc := New(discard, Services{}).api.OpenAPI()
	change := []string{"200", "401", "403", "404", "409", "422", "default"}
	for _, op := range []struct {
		operation *huma.Operation
		id        string
		statuses  []string
	}{
		{doc.Paths["/members"].Get, "listMembers", []string{"200", "401", "403", "default"}},
		{doc.Paths["/members/{id}/deactivate"].Post, "deactivateMember", change},
		{doc.Paths["/members/{id}/reactivate"].Post, "reactivateMember", change},
	} {
		operation := op.operation
		require.NotNil(t, operation, op.id)
		assert.Equal(t, op.id, operation.OperationID)
		assert.Equal(t, session.AccessAdmin, operation.Metadata[accessKey], op.id)
		var statuses []string
		for status := range operation.Responses {
			statuses = append(statuses, status)
		}
		assert.ElementsMatch(t, op.statuses, statuses, op.id)
	}
	responses := doc.Paths["/members/{id}/deactivate"].Post.Responses
	assert.Equal(t, problemPrefix+"member-not-found", responses["404"].Content[problemContentType].Example.(*problem).Type)
	assert.Equal(t, problemPrefix+"member-is-administrator", responses["409"].Content[problemContentType].Example.(*problem).Type)
	member := doc.Components.Schemas.Map()["Member"]
	require.NotNil(t, member)
	assert.ElementsMatch(t, []string{"id", "email", "role", "joinedAt", "emailVerified", "totpEnabled", "deactivatedAt", "allowedActions"}, member.Required)
	// A client draws one control per value, so the contract names them all.
	assert.Equal(t, []any{"deactivate", "reactivate"}, member.Properties["allowedActions"].Items.Enum)
}
