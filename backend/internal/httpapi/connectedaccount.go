package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"uuid"

	"amplop/internal/connectedaccount"
	"amplop/internal/mailsync"
	"amplop/internal/session"
)

const (
	connectedAccountsPath = "/connected-accounts"

	// googleCallbackPath is the address Google sends the member back to.
	// The setup guide tells administrators to register it, so it never
	// changes.
	googleCallbackPath = connectedAccountsPath + "/google/callback"

	// connectLandingPath is the web route the callback ends on, and
	// outcomeParam its one query parameter: the address carries only the
	// outcome.
	connectLandingPath = "/connected-accounts"
	outcomeParam       = "outcome"
)

// GoogleCallbackURL returns the address Google sends members back to for an
// amplop served at publicURL, which the Google client registers.
func GoogleCallbackURL(publicURL *url.URL) string {
	return publicURL.JoinPath(apiPrefix, googleCallbackPath).String()
}

type connectedAccountService interface {
	List(ctx context.Context, userID int64) ([]connectedaccount.Account, error)
	Rename(ctx context.Context, userID, accountID int64, name string) (connectedaccount.Account, error)
	Disconnect(ctx context.Context, userID, accountID int64) (connectedaccount.Account, error)
}

// syncService reports where accounts' synchronization stands and starts an
// account's sync when its member asks.
type syncService interface {
	Reports(ctx context.Context, accountIDs []int64) ([]mailsync.Report, error)
	Stopped(ctx context.Context) (bool, error)
	Request(ctx context.Context, userID, accountID int64) (mailsync.Report, error)
}

// connectionService runs the Gmail connect flow. It is nil when no Google
// client is configured, and Gmail is then not offered.
type connectionService interface {
	Start(ctx context.Context, userID int64, sessionID uuid.UUID, accountID int64) (string, error)
	Finish(ctx context.Context, userID int64, sessionID uuid.UUID, callback connectedaccount.Callback) (connectedaccount.Account, error)
}

type connectedAccountHandler struct {
	accounts    connectedAccountService
	connections connectionService
	providers   offeredProviders
	syncs       syncService
	sessions    sessionService
	logger      *slog.Logger
}

func registerConnectedAccounts(api huma.API, logger *slog.Logger, accounts connectedAccountService, connections connectionService, providers offeredProviders, syncs syncService, sessions sessionService) {
	handler := connectedAccountHandler{accounts: accounts, connections: connections, providers: providers, syncs: syncs, sessions: sessions, logger: logger}

	registerConnectedAccountList(api, handler)
	registerAccountSync(api, handler)
	registerAccountProviders(api, handler)
	registerGmailConnect(api, handler)
	registerAccountReconnect(api, handler)
	registerGoogleCallback(api, handler)
	registerAccountRename(api, handler)
	registerAccountDisconnect(api, handler)
}

const (
	gmailNotConfiguredType       = problemPrefix + "gmail-not-configured"
	connectThrottledType         = problemPrefix + "connect-throttled"
	connectedAccountNotFoundType = problemPrefix + "connected-account-not-found"
)

// connectedAccount is one of the member's connected accounts. Google's ID
// for the account stays on the server.
type connectedAccount struct {
	ID                  int64                     `json:"id"`
	Provider            connectedaccount.Provider `json:"provider" enum:"gmail"`
	Email               string                    `json:"email"`
	NeedsReconnectSince *time.Time                `json:"needsReconnectSince" nullable:"true" doc:"When Google stopped accepting the account's permission; null until it does. Reconnecting clears it."`
	Status              connectedaccount.Status   `json:"status" enum:"usable,needs-reconnect,token-unreadable" doc:"Whether amplop can use the account, as far as it knows now. usable: the saved permission opens and Google has not refused it, which it may still do at the next use. needs-reconnect: Google stopped accepting its permission, since needsReconnectSince. token-unreadable: no key amplop has opens the permission it saved, e.g. after a server key was lost; checked at each read, so restoring the key makes it usable again. Reconnecting fixes either."`
	CreatedAt           time.Time                 `json:"createdAt"`
	Name                *string                   `json:"name" nullable:"true" doc:"What the member calls the account, shown instead of its email; null until they choose one."`
	Color               int                       `json:"color" minimum:"1" maximum:"8" doc:"The account's colour, one of eight the web app defines, given at connect: the lowest the member's other accounts use least."`
}

func connectedAccountOf(account connectedaccount.Account) connectedAccount {
	return connectedAccount{
		ID:                  account.ID,
		Provider:            account.Provider,
		Email:               account.Email,
		NeedsReconnectSince: account.NeedsReconnectSince,
		Status:              account.Status,
		CreatedAt:           account.CreatedAt,
		Name:                accountName(account),
		Color:               account.Color,
	}
}

// accountName returns account's name, or nil when the member has chosen
// none.
func accountName(account connectedaccount.Account) *string {
	if account.Name == "" {
		return nil
	}
	return &account.Name
}

// connectedAccountProblem maps the errors every operation on one account
// shares.
func connectedAccountProblem(err error) error {
	switch {
	case errors.Is(err, connectedaccount.ErrNotFound):
		return newProblem(http.StatusNotFound, connectedAccountNotFoundType, "Account not found", "The connected account does not exist.")
	// The member was deactivated after the session was checked.
	case errors.Is(err, connectedaccount.ErrDeactivated):
		return unauthenticatedProblem()
	default:
		return err
	}
}

// memberResponses documents the failures every member operation shares.
func memberResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	responses := problemResponses(api, append([]int{http.StatusUnauthorized}, statuses...)...)
	unauthenticated := responses[strconv.Itoa(http.StatusUnauthorized)]
	unauthenticated.Description = unauthenticatedDescription
	unauthenticated.Content[problemContentType].Example = unauthenticatedProblem()
	return responses
}

// accountNotFoundResponse documents a 404 for an account id in responses.
func accountNotFoundResponse(responses map[string]*huma.Response) {
	notFound := responses[strconv.Itoa(http.StatusNotFound)]
	notFound.Description = "The signed-in account has no connected account with this id."
	notFound.Content[problemContentType].Example = connectedAccountProblem(connectedaccount.ErrNotFound)
}

// ========================
// Connected account list
// ========================

type connectedAccounts struct {
	Accounts []listedAccount `json:"accounts" nullable:"false" doc:"Oldest first."`
	// SyncStopped is the whole amplop's, not an account's.
	SyncStopped bool `json:"syncStopped" doc:"Whether syncing has stopped on this amplop: no run of the every-minute sync schedule succeeded in the last 5 minutes, e.g. as no worker runs. Only an administrator can fix it."`
}

// listedAccount is a [connectedAccount] with where its sync stands.
//
// NOTE: Huma documents no field of an embedded struct whose type is
// unexported, so the fields are repeated rather than embedded.
type listedAccount struct {
	ID                  int64                     `json:"id"`
	Provider            connectedaccount.Provider `json:"provider" enum:"gmail"`
	Email               string                    `json:"email"`
	NeedsReconnectSince *time.Time                `json:"needsReconnectSince" nullable:"true" doc:"When Google stopped accepting the account's permission; null until it does. Reconnecting clears it."`
	Status              connectedaccount.Status   `json:"status" enum:"usable,needs-reconnect,token-unreadable" doc:"Whether amplop can use the account, as far as it knows now. usable: the saved permission opens and Google has not refused it, which it may still do at the next use. needs-reconnect: Google stopped accepting its permission, since needsReconnectSince. token-unreadable: no key amplop has opens the permission it saved, e.g. after a server key was lost; checked at each read, so restoring the key makes it usable again. Reconnecting fixes either."`
	CreatedAt           time.Time                 `json:"createdAt"`
	Name                *string                   `json:"name" nullable:"true" doc:"What the member calls the account, shown instead of its email; null until they choose one."`
	Color               int                       `json:"color" minimum:"1" maximum:"8" doc:"The account's colour, one of eight the web app defines, given at connect: the lowest the member's other accounts use least."`
	Sync                accountSync               `json:"sync"`
	// Listed here, so every client offers only what the server accepts
	// instead of restating its rule (AGENTS.md, "API contract").
	AllowedActions []connectedaccount.Action `json:"allowedActions" nullable:"false" doc:"What the member can ask of this account now: sync (Sync Account Now), send (Send Message, Reply to Message, and Send Message Again from it), change-mail (Apply Conversation Action and Change Conversations on its conversations, and Create Label, Edit Label, and Delete Label in it), reconnect (Reconnect Account), rename (Rename Account), and disconnect (Disconnect Account). sync is missing while the account needs reconnecting, send and change-mail while its status is not usable, and sync, send, change-mail, and reconnect while its provider is not offered (see List Account Providers). A listed action can still be refused when the account or the instance's settings changed since this read. Clients ignore values they don't know."`
}

// TransformSchema lists allowedActions' values from [connectedaccount.Actions].
func (listedAccount) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	values := make([]any, 0, len(connectedaccount.Actions))
	for _, action := range connectedaccount.Actions {
		values = append(values, string(action))
	}
	s.Properties["allowedActions"].Items.Enum = values
	return s
}

// listedAccountOf returns account with report, its sync, in the response's
// shape; offered says whether its provider is offered.
func listedAccountOf(account connectedaccount.Account, report mailsync.Report, offered bool) listedAccount {
	return listedAccount{
		ID:                  account.ID,
		Provider:            account.Provider,
		Email:               account.Email,
		NeedsReconnectSince: account.NeedsReconnectSince,
		Status:              account.Status,
		CreatedAt:           account.CreatedAt,
		Name:                accountName(account),
		Color:               account.Color,
		Sync:                accountSyncOf(report),
		AllowedActions:      account.AllowedActions(offered),
	}
}

func registerConnectedAccountList(api huma.API, handler connectedAccountHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "listConnectedAccounts",
		Method:      http.MethodGet,
		Path:        connectedAccountsPath,
		Summary:     "List Connected Accounts",
		Tags:        []string{tagConnectedAccounts},
		Description: "Lists the signed-in account's connected Gmail accounts, each with where its sync stands, and whether syncing has stopped on this amplop.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   memberResponses(api),
	}, handler.handleListConnectedAccounts)
}

func (h connectedAccountHandler) handleListConnectedAccounts(ctx context.Context, _ *struct{}) (*output[connectedAccounts], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User

	// 1. List the member's accounts, which says whose sync states may be
	// read.
	accounts, err := h.accounts.List(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	// 2. Read their sync states. mailsync depends on connectedaccount, so
	// the two reads are composed here.
	ids := make([]int64, len(accounts))
	for i, account := range accounts {
		ids[i] = account.ID
	}
	reports, err := h.syncs.Reports(ctx, ids)
	if err != nil {
		return nil, err
	}
	stopped, err := h.syncs.Stopped(ctx)
	if err != nil {
		return nil, err
	}

	// 3. Pair them; an account disconnected between the reads has no sync
	// state left, and is left out.
	byAccount := make(map[int64]mailsync.Report, len(reports))
	for _, report := range reports {
		byAccount[report.AccountID] = report
	}
	listed := connectedAccounts{Accounts: make([]listedAccount, 0, len(accounts)), SyncStopped: stopped}
	for _, account := range accounts {
		report, ok := byAccount[account.ID]
		if !ok {
			continue
		}
		listed.Accounts = append(listed.Accounts, listedAccountOf(account, report, h.providers.offered(account.Provider)))
	}
	return respondData(listed), nil
}

// ========================
// Account providers
// ========================

// accountProviders is which providers members can connect accounts with.
type accountProviders struct {
	Offered []connectedaccount.Provider `json:"offered" nullable:"false" enum:"gmail" doc:"Providers an account can be connected or reconnected with now. Gmail only while an administrator has set up a Google client; empty when none is offered."`
}

func registerAccountProviders(api huma.API, handler connectedAccountHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "listAccountProviders",
		Method:      http.MethodGet,
		Path:        connectedAccountsPath + "/providers",
		Summary:     "List Account Providers",
		Tags:        []string{tagConnectedAccounts},
		Description: "Lists the providers the signed-in account can connect email accounts with, which depend on the instance's settings.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   memberResponses(api),
	}, handler.handleListAccountProviders)
}

func (h connectedAccountHandler) handleListAccountProviders(_ context.Context, _ *struct{}) (*output[accountProviders], error) {
	providers := accountProviders{Offered: []connectedaccount.Provider{}}
	if h.providers.offered(connectedaccount.ProviderGmail) {
		providers.Offered = append(providers.Offered, connectedaccount.ProviderGmail)
	}
	return respondData(providers), nil
}

// offeredProviders says which providers an administrator set up a client
// for, which every operation that reaches a provider asks.
type offeredProviders struct {
	gmail bool
}

// offered reports whether accounts can be connected with provider, and its
// accounts reconnected, synced, and sent from.
func (p offeredProviders) offered(provider connectedaccount.Provider) bool {
	return provider == connectedaccount.ProviderGmail && p.gmail
}

// ========================
// Gmail connect
// ========================

// authorization is where the browser goes next: Google's consent page.
type authorization struct {
	URL string `json:"url" format:"uri" doc:"Google's consent page. Open it in the same browser tab; Google then sends the browser back to amplop, which ends on the web app's /connected-accounts page with an outcome. The address works once, for 10 minutes, and only for this session."`
}

func registerGmailConnect(api huma.API, handler connectedAccountHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "connectGmail",
		Method:      http.MethodPost,
		Path:        connectedAccountsPath + "/google",
		Summary:     "Connect Gmail",
		Tags:        []string{tagConnectedAccounts},
		Description: "Starts connecting a Gmail account: returns the address of Google's consent page. " +
			"At most 5 connections or reconnections may be unfinished at once; each counts for 10 minutes unless finished.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: connectStartResponses(api),
	}, handler.handleConnectGmail)
}

func (h connectedAccountHandler) handleConnectGmail(ctx context.Context, _ *struct{}) (*output[authorization], error) {
	return h.start(ctx, 0)
}

// start begins the flow for the signed-in account; accountID is the
// account to reconnect, or 0 for a new one.
func (h connectedAccountHandler) start(ctx context.Context, accountID int64) (*output[authorization], error) {
	// Gmail is the only provider, so a reconnect's account is Gmail's too.
	if !h.providers.offered(connectedaccount.ProviderGmail) {
		return nil, connectStartProblem(errGmailNotConfigured)
	}
	current := ctx.Value(sessionKey{}).(session.Authenticated)
	consentURL, err := h.connections.Start(ctx, current.Identity.User.ID, current.Session.ID(), accountID)
	if err != nil {
		return nil, connectStartProblem(err)
	}
	return respondData(authorization{URL: consentURL}), nil
}

// errGmailNotConfigured means no Google client is configured.
var errGmailNotConfigured = errors.New("gmail is not configured")

func connectStartResponses(api huma.API) map[string]*huma.Response {
	responses := memberResponses(api, http.StatusConflict, http.StatusTooManyRequests)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No Google client is configured, so Gmail is not offered."
	conflict.Content[problemContentType].Example = connectStartProblem(errGmailNotConfigured)

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "Too many connections are unfinished."
	throttled.Content[problemContentType].Example = connectStartProblem(connectedaccount.ErrStartThrottled)
	return responses
}

func connectStartProblem(err error) error {
	switch {
	case errors.Is(err, errGmailNotConfigured):
		return newProblem(http.StatusConflict, gmailNotConfiguredType, "Gmail not configured", "Gmail can't be connected until an administrator sets up a Google client.")
	case errors.Is(err, connectedaccount.ErrStartThrottled):
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, connectThrottledType, "Too many connections", "Too many Gmail connections were started recently."), err)
	default:
		return connectedAccountProblem(err)
	}
}

// ========================
// Connected account reconnect
// ========================

type connectedAccountInput struct {
	ID int64 `path:"id" minimum:"1" doc:"Connected account ID from the list."`
}

func registerAccountReconnect(api huma.API, handler connectedAccountHandler) {
	responses := connectStartResponses(api)
	maps.Copy(responses, problemResponses(api, http.StatusNotFound))
	accountNotFoundResponse(responses)

	huma.Register(api, huma.Operation{
		OperationID: "reconnectConnectedAccount",
		Method:      http.MethodPost,
		Path:        connectedAccountsPath + "/{id}/reconnect",
		Summary:     "Reconnect Account",
		Tags:        []string{tagConnectedAccounts},
		Description: "Starts reconnecting a connected account, e.g. one that needs reconnecting: returns the address of Google's consent page. " +
			"The member must choose the same Google account there. Limits as for connecting.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleReconnectAccount)
}

func (h connectedAccountHandler) handleReconnectAccount(ctx context.Context, in *connectedAccountInput) (*output[authorization], error) {
	return h.start(ctx, in.ID)
}

// ========================
// Google callback
// ========================

// connectOutcome is how a connect flow ended, as the landing route's
// outcome parameter says.
type connectOutcome string

const (
	outcomeConnected        connectOutcome = "connected"
	outcomeCancelled        connectOutcome = "cancelled"
	outcomeGmailNotGranted  connectOutcome = "gmail-not-granted"
	outcomeEmailNotVerified connectOutcome = "email-not-verified"
	outcomeNoGmail          connectOutcome = "no-gmail"
	outcomeAlreadyConnected connectOutcome = "already-connected"
	outcomeDifferentAccount connectOutcome = "different-account"
	outcomeExpired          connectOutcome = "expired"
	outcomeFailed           connectOutcome = "failed"
)

// connectOutcomes lists every outcome, for the contract.
var connectOutcomes = []connectOutcome{
	outcomeConnected, outcomeCancelled, outcomeGmailNotGranted,
	outcomeEmailNotVerified, outcomeNoGmail, outcomeAlreadyConnected, outcomeDifferentAccount,
	outcomeExpired, outcomeFailed,
}

// googleCallbackInput is Google's redirect: the state, and either a code or
// an error. Its fields have no limits, so no input is refused with a body
// the browser would show instead of the landing route.
type googleCallbackInput struct {
	State string `query:"state" doc:"The state amplop sent to Google."`
	Code  string `query:"code" doc:"The authorization code, when the member agreed."`
	Error string `query:"error" doc:"Google's error, e.g. access_denied when the member cancelled."`

	// secret is the session cookie's value; empty when there is none.
	secret string
}

// Resolve reads the session cookie: the operation checks the session itself,
// so a refused one still ends on the landing route.
func (in *googleCallbackInput) Resolve(ctx huma.Context) []error {
	if cookie, err := huma.ReadCookie(ctx, sessionCookieName); err == nil {
		in.secret = cookie.Value
	}
	return nil
}

// landing is the redirect to the landing route.
type landing struct {
	Location     string `header:"Location" doc:"The web app's /connected-accounts page with an outcome parameter."`
	CacheControl string `header:"Cache-Control"`
}

func registerGoogleCallback(api huma.API, handler connectedAccountHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "finishGoogleConnect",
		Method:      http.MethodGet,
		Path:        googleCallbackPath,
		Summary:     "Finish Gmail Connection",
		Tags:        []string{tagRedirects},
		Description: "Google sends the browser here after its consent page; clients never call it. " +
			"It saves the connected account and redirects to the web app's /connected-accounts page, whose outcome parameter is a " +
			connectOutcomeSchemaName + ". Every outcome is a redirect, including a missing or ended session. The state works once.",
		// Public to the access check: a refused session must still end on
		// the landing route, not on a Problem Details body. The handler
		// checks the session, and the state is bound to it.
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		DefaultStatus: http.StatusSeeOther,
		Responses:     map[string]*huma.Response{"default": {Description: "Other errors. See Shared errors in the API overview."}},
		// The landing route reads the outcome from its address, which no
		// response body carries. The reference names the outcomes' schema
		// and keeps it in the contract, which drops schemas nothing
		// references, for the web to type them.
		Extensions: map[string]any{"x-landing-outcome": map[string]any{"$ref": "#/components/schemas/" + connectOutcomeSchemaName}},
	}, handler.handleGoogleCallback)

	enum := make([]any, len(connectOutcomes))
	for i, outcome := range connectOutcomes {
		enum[i] = string(outcome)
	}
	api.OpenAPI().Components.Schemas.Map()[connectOutcomeSchemaName] = &huma.Schema{
		Type:        huma.TypeString,
		Description: "How a Gmail connection ended, as the outcome parameter of the web app's /connected-accounts page says.",
		Enum:        enum,
	}
}

// connectOutcomeSchemaName names the contract's list of connect outcomes.
const connectOutcomeSchemaName = "ConnectOutcome"

func (h connectedAccountHandler) handleGoogleCallback(ctx context.Context, in *googleCallbackInput) (*landing, error) {
	outcome := h.finish(ctx, in)
	// A relative reference: the browser resolves it against amplop's own
	// address, whatever the request's Host header says.
	location := connectLandingPath + "?" + url.Values{outcomeParam: {string(outcome)}}.Encode()
	// The redirect is the only record of a code's use; caching it would
	// replay a stale outcome.
	return &landing{Location: location, CacheControl: "no-store"}, nil
}

// finish ends the flow in's state started and returns its outcome. It logs
// failures the member cannot fix, never the code, state, or query string.
func (h connectedAccountHandler) finish(ctx context.Context, in *googleCallbackInput) connectOutcome {
	// The flow never runs, so the state is not used: it still expires in
	// minutes, bound to its session and PKCE verifier.
	if h.connections == nil {
		return outcomeFailed
	}

	// 1. Identify the session. Without one, or when the check fails, the
	// state is still taken below, so it cannot be used again; it then fails
	// the session check.
	var userID int64
	var sessionID uuid.UUID
	var sessionErr error
	if in.secret != "" {
		current, err := h.sessions.Current(ctx, in.secret)
		switch {
		case err == nil && memberAllowed(current.Identity):
			userID, sessionID = current.Identity.User.ID, current.Session.ID()
		case err != nil && !errors.Is(err, session.ErrUnauthenticated):
			// No session to finish with; the state is still taken below.
			h.logger.ErrorContext(ctx, "gmail connect failed", "err", err)
			sessionErr = err
		}
	}

	// 2. Finish the flow. A reconnect ends as connected too.
	_, err := h.connections.Finish(ctx, userID, sessionID, connectedaccount.Callback{State: in.State, Code: in.Code, Error: in.Error})
	if sessionErr != nil {
		return outcomeFailed
	}
	if err == nil {
		return outcomeConnected
	}
	outcome := callbackOutcome(err)
	if outcome == outcomeFailed {
		h.logger.WarnContext(ctx, "gmail connect failed", "err", err)
	}
	return outcome
}

// callbackOutcome returns the outcome of a flow [connectedaccount.Connections.Finish]
// ended with err.
func callbackOutcome(err error) connectOutcome {
	switch {
	case errors.Is(err, connectedaccount.ErrStateInvalid):
		return outcomeExpired
	case errors.Is(err, connectedaccount.ErrCancelled):
		return outcomeCancelled
	case errors.Is(err, connectedaccount.ErrGmailNotGranted):
		return outcomeGmailNotGranted
	case errors.Is(err, connectedaccount.ErrEmailNotVerified):
		return outcomeEmailNotVerified
	case errors.Is(err, connectedaccount.ErrNoGmail):
		return outcomeNoGmail
	case errors.Is(err, connectedaccount.ErrAlreadyConnected):
		return outcomeAlreadyConnected
	case errors.Is(err, connectedaccount.ErrDifferentAccount):
		return outcomeDifferentAccount
	default:
		return outcomeFailed
	}
}

// ========================
// Connected account rename
// ========================

type accountRenameInput struct {
	ID   int64 `path:"id" minimum:"1" doc:"Connected account ID from the list."`
	Body struct {
		Name string `json:"name" maxLength:"64" doc:"The account's new name, at most 64 characters as sent. The white space around it is dropped; empty, or only white space, clears it."`
	}
}

type renamedAccount struct {
	Account connectedAccount `json:"account"`
}

func registerAccountRename(api huma.API, handler connectedAccountHandler) {
	responses := memberResponses(api, http.StatusNotFound, http.StatusUnprocessableEntity)
	accountNotFoundResponse(responses)

	huma.Register(api, huma.Operation{
		OperationID: "renameConnectedAccount",
		Method:      http.MethodPatch,
		Path:        connectedAccountsPath + "/{id}",
		Summary:     "Rename Account",
		Tags:        []string{tagConnectedAccounts},
		Description: "Sets the name the web app shows for a connected account instead of its email, or clears it. Only amplop keeps the name; Google's account is unchanged.",
		Metadata:    map[string]any{accessKey: session.AccessMember},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   responses,
	}, handler.handleRenameAccount)
}

func (h connectedAccountHandler) handleRenameAccount(ctx context.Context, in *accountRenameInput) (*output[renamedAccount], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	account, err := h.accounts.Rename(ctx, user.ID, in.ID, in.Body.Name)
	if err != nil {
		return nil, accountRenameProblem(err)
	}
	return respondData(renamedAccount{Account: connectedAccountOf(account)}), nil
}

// accountRenameProblem maps the errors of renaming an account.
func accountRenameProblem(err error) error {
	var message string
	switch {
	case errors.Is(err, connectedaccount.ErrNameTooLong):
		// Huma's own maxLength failure reads the same, so the response does
		// not depend on which check refused the name.
		message = fmt.Sprintf("Use at most %d characters.", connectedaccount.MaxNameLength)
	case errors.Is(err, connectedaccount.ErrNameInvalid):
		message = "Enter a name without line breaks or other control characters."
	default:
		return connectedAccountProblem(err)
	}
	invalid := requestProblem(http.StatusUnprocessableEntity)
	invalid.Errors = []inputError{{Location: "body.name", Message: message}}
	return invalid
}

// ========================
// Connected account disconnect
// ========================

type disconnectedAccount struct {
	Account connectedAccount `json:"account" doc:"The account as it was before disconnecting."`
}

func registerAccountDisconnect(api huma.API, handler connectedAccountHandler) {
	responses := memberResponses(api, http.StatusNotFound)
	accountNotFoundResponse(responses)

	huma.Register(api, huma.Operation{
		OperationID: "disconnectConnectedAccount",
		Method:      http.MethodDelete,
		Path:        connectedAccountsPath + "/{id}",
		Summary:     "Disconnect Account",
		Tags:        []string{tagConnectedAccounts},
		Description: "Deletes a connected account and everything amplop keeps for it. " +
			"Afterwards amplop asks Google to end its permission, unless another member connected the same Google account.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleDisconnectAccount)
}

func (h connectedAccountHandler) handleDisconnectAccount(ctx context.Context, in *connectedAccountInput) (*output[disconnectedAccount], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	account, err := h.accounts.Disconnect(ctx, user.ID, in.ID)
	if err != nil {
		return nil, connectedAccountProblem(err)
	}
	return respondData(disconnectedAccount{Account: connectedAccountOf(account)}), nil
}
