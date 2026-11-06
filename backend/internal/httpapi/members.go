package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/account"
	"inbok/internal/session"
)

const membersPath = "/members"

const (
	memberNotFoundType        = problemPrefix + "member-not-found"
	memberIsAdministratorType = problemPrefix + "member-is-administrator"
)

type membersService interface {
	List(ctx context.Context) ([]account.Member, error)
	Deactivate(ctx context.Context, actorID, memberID int64) (account.Member, error)
	Reactivate(ctx context.Context, actorID, memberID int64) (account.Member, error)
}

type membersHandler struct {
	service membersService
}

func registerMembers(api huma.API, service membersService) {
	handler := membersHandler{service: service}

	registerMemberList(api, handler)
	registerMemberDeactivation(api, handler)
	registerMemberReactivation(api, handler)
}

// member is an account as the member list shows it, administrators included.
// It holds nothing about the account's mail or activity.
type member struct {
	ID            int64        `json:"id" minimum:"1"`
	Email         string       `json:"email"`
	Role          account.Role `json:"role" enum:"member,admin"`
	JoinedAt      time.Time    `json:"joinedAt"`
	EmailVerified bool         `json:"emailVerified"`
	TOTPEnabled   bool         `json:"totpEnabled" doc:"Whether a confirmed authenticator protects sign-in."`
	DeactivatedAt *time.Time   `json:"deactivatedAt" nullable:"true" doc:"When the account was deactivated; null while it is active."`
}

// memberOf returns the response form of m.
func memberOf(m account.Member) member {
	return member{
		ID: m.ID, Email: m.Email, Role: m.Role, JoinedAt: m.JoinedAt,
		EmailVerified: m.EmailVerified, TOTPEnabled: m.TOTPEnabled, DeactivatedAt: m.DeactivatedAt,
	}
}

// memberChangeInput names the member a deactivation or reactivation acts on.
type memberChangeInput struct {
	ID int64 `path:"id" minimum:"1" doc:"Member ID from the member list."`
}

// changedMember is the member as a deactivation or reactivation saved it.
type changedMember struct {
	Member member `json:"member"`
}

// memberChangeResponses returns the responses deactivation and reactivation
// document beyond success.
func memberChangeResponses(api huma.API) map[string]*huma.Response {
	responses := adminResponses(api, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity)

	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "No account has this ID."
	notFound.Content[problemContentType].Example = memberChangeProblem(account.ErrUserNotFound)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "The account is an administrator's. Administrators can't be deactivated or reactivated."
	conflict.Content[problemContentType].Example = memberChangeProblem(account.ErrAdminTarget)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The ID is not a positive whole number."
	invalid.Content[problemContentType].Example = requestProblem(http.StatusUnprocessableEntity)

	return responses
}

// memberChangeProblem returns the public problem for a known deactivation or
// reactivation error, or err unchanged.
func memberChangeProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrUserNotFound):
		return newProblem(http.StatusNotFound, memberNotFoundType, "Member not found", "This account doesn't exist.")
	case errors.Is(err, account.ErrAdminTarget):
		return newProblem(http.StatusConflict, memberIsAdministratorType, "Member is an administrator", "Administrators can't be deactivated or reactivated.")
	default:
		return err
	}
}

// ========================
// Member list
// ========================

type listedMembers struct {
	Members []member `json:"members" nullable:"false" doc:"Every account, oldest first."`
}

func registerMemberList(api huma.API, handler membersHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "listMembers",
		Method:      http.MethodGet,
		Path:        membersPath,
		Summary:     "List Members",
		Tags:        []string{tagMembers},
		Description: "Lists every account, administrators included, oldest first.",
		Metadata:    map[string]any{accessKey: session.AccessAdmin},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   adminResponses(api),
	}, handler.handleListMembers)
}

func (h membersHandler) handleListMembers(ctx context.Context, _ *struct{}) (*output[listedMembers], error) {
	members, err := h.service.List(ctx)
	if err != nil {
		return nil, err
	}
	listed := listedMembers{Members: make([]member, 0, len(members))}
	for _, m := range members {
		listed.Members = append(listed.Members, memberOf(m))
	}
	return respondData(listed), nil
}

// ========================
// Member deactivation
// ========================

func registerMemberDeactivation(api huma.API, handler membersHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "deactivateMember",
		Method:      http.MethodPost,
		Path:        membersPath + "/{id}/deactivate",
		Summary:     "Deactivate Member",
		Tags:        []string{tagMembers},
		Description: "Ends the member's sessions and stops them from signing in until they are reactivated. " +
			"Their account, password, and authenticator are kept. Deactivating a deactivated member changes nothing. " +
			"The change is recorded in the audit log.",
		Metadata:  map[string]any{accessKey: session.AccessAdmin},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: memberChangeResponses(api),
	}, handler.handleDeactivateMember)
}

func (h membersHandler) handleDeactivateMember(ctx context.Context, in *memberChangeInput) (*output[changedMember], error) {
	// The actor is the checked session's account, never a request value.
	actor := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	changed, err := h.service.Deactivate(ctx, actor.ID, in.ID)
	if err != nil {
		return nil, memberChangeProblem(err)
	}
	return respondData(changedMember{Member: memberOf(changed)}), nil
}

// ========================
// Member reactivation
// ========================

func registerMemberReactivation(api huma.API, handler membersHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "reactivateMember",
		Method:      http.MethodPost,
		Path:        membersPath + "/{id}/reactivate",
		Summary:     "Reactivate Member",
		Tags:        []string{tagMembers},
		Description: "Lets a deactivated member sign in again with their existing password and authenticator. " +
			"Reactivating an active member changes nothing. The change is recorded in the audit log.",
		Metadata:  map[string]any{accessKey: session.AccessAdmin},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: memberChangeResponses(api),
	}, handler.handleReactivateMember)
}

func (h membersHandler) handleReactivateMember(ctx context.Context, in *memberChangeInput) (*output[changedMember], error) {
	// The actor is the checked session's account, never a request value.
	actor := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	changed, err := h.service.Reactivate(ctx, actor.ID, in.ID)
	if err != nil {
		return nil, memberChangeProblem(err)
	}
	return respondData(changedMember{Member: memberOf(changed)}), nil
}
