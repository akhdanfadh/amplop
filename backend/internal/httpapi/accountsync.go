package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/connectedaccount"
	"inbok/internal/mailsync"
	"inbok/internal/session"
)

// ========================
// Account sync
// ========================

const (
	accountNeedsReconnectType = problemPrefix + "account-needs-reconnect"
	syncThrottledType         = problemPrefix + "sync-throttled"
)

// accountSync is where a connected account's sync stands, as raw fields the
// web words (ADR001, "Polling").
type accountSync struct {
	Status        mailsync.Status `json:"status" enum:"waiting,running,complete,failed" doc:"waiting: the first full sync, or the next after a reconnect, has not started. running: a full sync copies the last 90 days; new mail still arrives. complete: the copy is current, and kept so every minute. failed: the last full sync failed; inbok tries again an hour later."`
	MessagesSoFar *int            `json:"messagesSoFar" nullable:"true" doc:"How many messages the running full sync has copied; null unless status is running. Gmail gives no total."`
	LastSyncedAt  *time.Time      `json:"lastSyncedAt" nullable:"true" doc:"When inbok last confirmed its copy matched Gmail: a sync finished, or the every-minute check found nothing new; null before the first."`
	Failure       *string         `json:"failure" nullable:"true" enum:"provider,token-unreadable,internal" doc:"Why the last sync, full or poll, failed; null once one succeeds. provider: Gmail kept failing, and inbok tries again. token-unreadable: the saved permission can no longer be read. internal: inbok failed. A complete account keeps a failed poll's."`
	FailedAt      *time.Time      `json:"failedAt" nullable:"true" doc:"When failure happened; null exactly when it is."`
}

// TransformSchema adds null to failure's values: Huma lists only the enum
// tag's, so a validator would refuse a healthy account's null.
func (accountSync) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	if failure := s.Properties["failure"]; failure != nil && !slices.Contains(failure.Enum, nil) {
		failure.Enum = append(failure.Enum, nil)
	}
	return s
}

// accountSyncOf converts a report to its response.
func accountSyncOf(report mailsync.Report) accountSync {
	sync := accountSync{
		Status:       report.Status,
		LastSyncedAt: report.LastSyncedAt,
		FailedAt:     report.FailedAt,
	}
	if report.Status == mailsync.StatusRunning {
		sync.MessagesSoFar = &report.MessagesSoFar
	}
	if report.Failure != "" {
		failure := string(report.Failure)
		sync.Failure = &failure
	}
	return sync
}

func registerAccountSync(api huma.API, handler connectedAccountHandler) {
	responses := memberResponses(api, http.StatusNotFound, http.StatusConflict, http.StatusTooManyRequests)
	accountNotFoundResponse(responses)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "Google stopped accepting the account's permission, so reconnect it first; or no Google client is configured, so Gmail is not offered."
	conflict.Content[problemContentType].Examples = map[string]*huma.Example{
		"needsReconnect":     {Value: accountSyncProblem(connectedaccount.ErrNeedsReconnect)},
		"gmailNotConfigured": {Value: accountSyncProblem(errGmailNotConfigured)},
	}

	throttled := responses[strconv.Itoa(http.StatusTooManyRequests)]
	throttled.Description = "The account's sync was requested in the last 15 seconds."
	throttled.Content[problemContentType].Example = accountSyncProblem(mailsync.ErrRequestThrottled)

	huma.Register(api, huma.Operation{
		OperationID: "syncConnectedAccount",
		Method:      http.MethodPost,
		Path:        connectedAccountsPath + "/{id}/sync",
		Summary:     "Sync Account Now",
		Tags:        []string{tagConnectedAccounts},
		Description: "Starts a connected account's sync now instead of at the next minute: a check for new mail, or a full sync when one is due. " +
			"A sync already waiting or running keeps its place, with the same answer; it checks for mail again before it ends, or the next minute's check does. " +
			"One request per account every 15 seconds. The account's allowedActions in the list says whether it can sync now.",
		Metadata:      map[string]any{accessKey: session.AccessMember},
		Security:      []map[string][]string{{sessionSecuritySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
		Responses:     responses,
	}, handler.handleSyncAccount)
}

func (h connectedAccountHandler) handleSyncAccount(ctx context.Context, in *connectedAccountInput) (*output[accountSync], error) {
	// 1. Refuse before the request is saved: without the client no worker
	// syncs Gmail, so an accepted request would wait forever (ADR001,
	// "Polling"). Gmail is the only provider, so the account is Gmail's.
	if !h.offered(connectedaccount.ProviderGmail) {
		return nil, accountSyncProblem(errGmailNotConfigured)
	}

	// 2. Save the request and start the sync.
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	report, err := h.syncs.Request(ctx, user.ID, in.ID)
	if err != nil {
		return nil, accountSyncProblem(err)
	}
	return respondData(accountSyncOf(report)), nil
}

func accountSyncProblem(err error) error {
	switch {
	case errors.Is(err, mailsync.ErrNotFound):
		return connectedAccountProblem(connectedaccount.ErrNotFound)
	case errors.Is(err, errGmailNotConfigured):
		return newProblem(http.StatusConflict, gmailNotConfiguredType, "Gmail not configured", "Gmail accounts can't sync until an administrator sets up a Google client.")
	case errors.Is(err, connectedaccount.ErrNeedsReconnect):
		return newProblem(http.StatusConflict, accountNeedsReconnectType, "Account needs reconnecting", "Google stopped accepting this account's permission. Reconnect it to sync.")
	case errors.Is(err, mailsync.ErrRequestThrottled):
		return retryAfterProblem(newProblem(http.StatusTooManyRequests, syncThrottledType, "Sync requested recently", "This account's sync was requested moments ago."), err)
	default:
		return connectedAccountProblem(err)
	}
}
