package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/account"
	"inbok/internal/session"
)

const (
	setupPath             = "/setup"
	setupDoneType         = problemPrefix + "setup-done"
	setupUnconfiguredType = problemPrefix + "setup-unconfigured"
	setupTokenInvalidType = problemPrefix + "setup-token-invalid"
)

// newSetup is the request body. Huma checks its fields before calling Setup.
type newSetup struct {
	Token    string `json:"token" writeOnly:"true"`
	Email    string `json:"email" format:"email" minLength:"3" maxLength:"254"`
	Password string `json:"password" writeOnly:"true" minLength:"15" maxLength:"128"`
}

// setupInput combines setup fields with the resolved client address.
type setupInput struct {
	Body newSetup
	addr netip.Addr
}

// Resolve fills the client address for throttling using Gin's proxy rules.
// Huma calls it automatically after parsing the request, before the handler.
func (in *setupInput) Resolve(ctx huma.Context) []error {
	return resolveClientAddr(ctx, &in.addr)
}

// setupThrottle reserves setup failures against the shared address limit.
type setupThrottle interface {
	BeginSetup(ctx context.Context, addr netip.Addr) (int64, error)
	Succeed(ctx context.Context, id int64) error
}

type setupStatus struct {
	Status account.SetupStatus `json:"status" enum:"unconfigured,open,done"`
}

type firstAdmin struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// accountService is the account behavior called by these HTTP operations.
type accountService interface {
	SetupStatus(ctx context.Context) (account.SetupStatus, error)
	Setup(ctx context.Context, input account.SetupInput) (account.User, error)
}

func registerAccount(api huma.API, service accountService, throttle setupThrottle) {
	huma.Register(api, huma.Operation{
		OperationID: "getSetupStatus",
		Metadata:    map[string]any{accessKey: session.AccessPublic},
		Method:      http.MethodGet,
		Path:        setupPath,
		Summary:     "Get first-admin setup status",
		Responses:   problemResponses(api),
	}, func(ctx context.Context, _ *struct{}) (*output[setupStatus], error) {
		status, err := service.SetupStatus(ctx)
		if err != nil {
			return nil, err
		}
		return respondData(setupStatus{Status: status}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "createFirstAdmin",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Method:        http.MethodPost,
		Path:          setupPath,
		Summary:       "Create the first administrator",
		DefaultStatus: http.StatusCreated,
		Responses:     problemResponses(api, http.StatusForbidden, http.StatusConflict, http.StatusTooManyRequests),
	}, func(ctx context.Context, in *setupInput) (*output[firstAdmin], error) {
		id, err := throttle.BeginSetup(ctx, in.addr)
		if err != nil {
			return nil, throttleProblem(err)
		}

		user, err := service.Setup(ctx, account.SetupInput{
			Token:    in.Body.Token,
			Email:    in.Body.Email,
			Password: in.Body.Password,
		})
		if err != nil {
			return nil, setupProblem(err)
		}
		if err := throttle.Succeed(ctx, id); err != nil {
			return nil, err
		}
		return respondData(firstAdmin{ID: user.ID, Email: user.Email}), nil
	})

	post := api.OpenAPI().Paths[setupPath].Post
	conflict := post.Responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Setup is complete or no setup token is configured."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"setupDone":         {Value: setupProblem(account.ErrSetupDone)},
		"setupUnconfigured": {Value: setupProblem(account.ErrSetupUnconfigured)},
	}
	forbidden := post.Responses[strconv.Itoa(http.StatusForbidden)]
	forbidden.Description = "The setup token is invalid."
	forbidden.Content[problemContentType].Example = setupProblem(account.ErrSetupTokenInvalid)
	throttled := post.Responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many recent failed attempts from this address."
	throttled.Content[problemContentType].Example = throttleProblem(session.ErrThrottled)
}

func setupProblem(err error) error {
	switch {
	case errors.Is(err, account.ErrSetupDone):
		return newProblem(http.StatusConflict, setupDoneType, "Setup complete", "An administrator has already been created.")
	case errors.Is(err, account.ErrSetupUnconfigured):
		return newProblem(http.StatusConflict, setupUnconfiguredType, "Setup unavailable", "Configure INBOK_SETUP_TOKEN to enable setup.")
	case errors.Is(err, account.ErrSetupTokenInvalid):
		return newProblem(http.StatusForbidden, setupTokenInvalidType, "Setup token invalid", "The setup token is invalid.")
	default:
		return err
	}
}
