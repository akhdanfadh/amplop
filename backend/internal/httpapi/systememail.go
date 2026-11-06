package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/emailaddress"
	"inbok/internal/session"
	"inbok/internal/systememail"
)

const systemEmailPath = "/system-email"

type systemEmailService interface {
	Settings(ctx context.Context) (*systememail.Settings, error)
	Save(ctx context.Context, actorID int64, input systememail.Input) (systememail.Settings, error)
}

type testMessageService interface {
	Send(ctx context.Context, recipient string) (systememail.TestMessageResult, error)
}

type systemEmailHandler struct {
	service      systemEmailService
	testMessages testMessageService
}

func registerSystemEmail(api huma.API, service systemEmailService, testMessages testMessageService) {
	handler := systemEmailHandler{service: service, testMessages: testMessages}

	registerSystemEmailSettings(api, handler)
	registerSystemEmailSave(api, handler)
	registerSystemEmailTestMessage(api, handler)
}

// ========================
// System email settings
// ========================

type systemEmail struct {
	Settings *systemEmailSettings `json:"settings,omitempty" doc:"Absent until an administrator saves settings."`
}

type systemEmailSettings struct {
	Host        string               `json:"host"`
	Port        int                  `json:"port"`
	Security    systememail.Security `json:"security" enum:"tls,starttls,none"`
	Username    string               `json:"username" doc:"Empty when the server needs no sign-in."`
	HasPassword bool                 `json:"hasPassword" doc:"Whether a password is saved. The password itself is never returned."`
	FromAddress string               `json:"fromAddress"`
	FromName    string               `json:"fromName"`
	TestedAt    *time.Time           `json:"testedAt" nullable:"true" doc:"When these settings last passed a test message; null if they have not."`
}

func registerSystemEmailSettings(api huma.API, handler systemEmailHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "getSystemEmail",
		Method:      http.MethodGet,
		Path:        systemEmailPath,
		Summary:     "Get System Email Settings",
		Tags:        []string{tagSystemEmail},
		Description: "The instance's own sender, separate from members' mailboxes. The password is never returned.",
		Metadata:    map[string]any{accessKey: session.AccessAdmin},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   adminResponses(api),
	}, handler.handleGetSystemEmail)
}

func (h systemEmailHandler) handleGetSystemEmail(ctx context.Context, _ *struct{}) (*output[systemEmail], error) {
	settings, err := h.service.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return respondData(systemEmail{}), nil
	}
	return respondData(systemEmail{Settings: systemEmailSettingsOf(*settings)}), nil
}

func systemEmailSettingsOf(settings systememail.Settings) *systemEmailSettings {
	return &systemEmailSettings{
		Host:        settings.Host,
		Port:        settings.Port,
		Security:    settings.Security,
		Username:    settings.Username,
		HasPassword: settings.HasPassword,
		FromAddress: settings.FromAddress,
		FromName:    settings.FromName,
		TestedAt:    settings.TestedAt,
	}
}

// ========================
// System email save
// ========================

// newSystemEmailSettings is the save request body. The web app words its
// limits as field errors.
type newSystemEmailSettings struct {
	Host        string               `json:"host" maxLength:"253" pattern:"\\S" patternDescription:"not blank"`
	Port        int                  `json:"port" minimum:"1" maximum:"65535"`
	Security    systememail.Security `json:"security" enum:"tls,starttls,none"`
	Username    string               `json:"username,omitempty" maxLength:"254" doc:"Omit or leave empty for a server that needs no sign-in."`
	Password    string               `json:"password,omitempty" writeOnly:"true" maxLength:"255" doc:"Omit or leave empty to keep the saved password, if the host, port, and username are unchanged."`
	FromAddress string               `json:"fromAddress" format:"email" minLength:"3" maxLength:"254" doc:"Letters A to Z are saved in lowercase."`
	FromName    string               `json:"fromName" maxLength:"64" pattern:"\\S" patternDescription:"not blank"`
}

func registerSystemEmailSave(api huma.API, handler systemEmailHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "saveSystemEmail",
		Method:      http.MethodPut,
		Path:        systemEmailPath,
		Summary:     "Save System Email Settings",
		Tags:        []string{tagSystemEmail},
		Description: "Replaces the saved settings. Saving clears the tested time until a test message passes. The save is recorded in the audit log, without setting values.",
		Metadata:    map[string]any{accessKey: session.AccessAdmin},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   systemEmailSaveResponses(api),
	}, handler.handleSaveSystemEmail)
}

func (h systemEmailHandler) handleSaveSystemEmail(ctx context.Context, in *struct{ Body newSystemEmailSettings }) (*output[systemEmail], error) {
	// The actor is the checked session's account, never a request value.
	actor := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User
	// Huma's patterns accept surrounding spaces, which DNS and SASL would not;
	// a blank username means no sign-in. The service checks the From address.
	saved, err := h.service.Save(ctx, actor.ID, systememail.Input{
		Server: systememail.Server{
			Host:     strings.TrimSpace(in.Body.Host),
			Port:     in.Body.Port,
			Security: in.Body.Security,
			Username: strings.TrimSpace(in.Body.Username),
		},
		Password:    in.Body.Password,
		FromAddress: in.Body.FromAddress,
		FromName:    in.Body.FromName,
	})
	if err != nil {
		return nil, systemEmailSaveProblem(err)
	}
	return respondData(systemEmail{Settings: systemEmailSettingsOf(saved)}), nil
}

func systemEmailSaveResponses(api huma.API) map[string]*huma.Response {
	responses := adminResponses(api, http.StatusUnprocessableEntity)
	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The request inputs are invalid or do not fit together."
	invalid.Content[problemContentType].Examples = map[string]*huma.Example{
		"cleartextUsername":       {Value: systemEmailSaveProblem(systememail.ErrCleartextUsername)},
		"passwordWithoutUsername": {Value: systemEmailSaveProblem(systememail.ErrPasswordWithoutUsername)},
		"passwordRequired":        {Value: systemEmailSaveProblem(systememail.ErrPasswordRequired)},
		"invalidInput":            {Value: requestProblem(http.StatusUnprocessableEntity)},
	}
	return responses
}

func systemEmailSaveProblem(err error) error {
	var input inputError
	switch {
	case errors.Is(err, systememail.ErrCleartextUsername):
		input = inputError{Location: "body.security", Message: "Choose TLS or STARTTLS to sign in with a username."}
	case errors.Is(err, systememail.ErrPasswordWithoutUsername):
		input = inputError{Location: "body.username", Message: "Enter the username for this password."}
	case errors.Is(err, systememail.ErrPasswordRequired):
		input = inputError{Location: "body.password", Message: "Enter the password for this server and username."}
	case errors.Is(err, systememail.ErrInvalidHost):
		input = inputError{Location: "body.host", Message: "Enter a host name without control characters."}
	case errors.Is(err, systememail.ErrInvalidUsername):
		input = inputError{Location: "body.username", Message: "Enter a username without control characters."}
	case errors.Is(err, systememail.ErrInvalidPassword):
		input = inputError{Location: "body.password", Message: "Enter a password of at most 255 bytes, without control characters."}
	case errors.Is(err, systememail.ErrInvalidFromName):
		input = inputError{Location: "body.fromName", Message: "Enter a name without control characters."}
	case errors.Is(err, emailaddress.ErrInvalid):
		return invalidEmailProblem("body.fromAddress")
	default:
		return err
	}
	invalid := requestProblem(http.StatusUnprocessableEntity)
	invalid.Errors = []inputError{input}
	return invalid
}

// ========================
// Test message
// ========================

const systemEmailNotConfiguredType = problemPrefix + "system-email-not-configured"

type systemEmailTestMessageRecipient struct {
	Recipient string `json:"recipient" format:"email" maxLength:"254" doc:"Where to send the test message. Letters A to Z are sent in lowercase."`
}

// testMessageStatus is whether the server accepted the test message.
type testMessageStatus string

const (
	testMessageAccepted testMessageStatus = "accepted"
	testMessageFailed   testMessageStatus = "failed"
)

type systemEmailTestMessageResult struct {
	Status testMessageStatus  `json:"status" enum:"accepted,failed" doc:"Accepted means the server took the message for delivery, not that it arrived."`
	Reason systememail.Reason `json:"reason,omitempty" enum:"connect,tls,auth,sender,recipient,timeout" doc:"The step that failed. Present only when status is failed."`
}

func registerSystemEmailTestMessage(api huma.API, handler systemEmailHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "sendSystemEmailTestMessage",
		Method:      http.MethodPost,
		Path:        systemEmailPath + "/test-message",
		Summary:     "Send Test Message",
		Tags:        []string{tagSystemEmail},
		Description: "Sends a test message with the saved settings. A server or network refusal is a failed result, not an error. A pass records the tested time; a failure clears it. Test messages are not audited.",
		Metadata:    map[string]any{accessKey: session.AccessAdmin},
		Security:    []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses:   testMessageResponses(api),
	}, handler.handleSendTestMessage)
}

func (h systemEmailHandler) handleSendTestMessage(ctx context.Context, in *struct {
	Body systemEmailTestMessageRecipient
}) (*output[systemEmailTestMessageResult], error) {
	result, err := h.testMessages.Send(ctx, in.Body.Recipient)
	if err != nil {
		return nil, testMessageProblem(err)
	}
	if !result.Accepted {
		return respondData(systemEmailTestMessageResult{Status: testMessageFailed, Reason: result.Reason}), nil
	}
	return respondData(systemEmailTestMessageResult{Status: testMessageAccepted}), nil
}

func testMessageResponses(api huma.API) map[string]*huma.Response {
	responses := adminResponses(api, http.StatusConflict, http.StatusUnprocessableEntity)

	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "No system email settings are saved."
	conflict.Content[problemContentType].Example = testMessageProblem(systememail.ErrNotConfigured)

	invalid := responses[strconv.Itoa(http.StatusUnprocessableEntity)]
	invalid.Description = "The recipient is missing or not a single email address."
	invalid.Content[problemContentType].Example = requestProblem(http.StatusUnprocessableEntity)
	return responses
}

func testMessageProblem(err error) error {
	if errors.Is(err, emailaddress.ErrInvalid) {
		return invalidEmailProblem("body.recipient")
	}
	if errors.Is(err, systememail.ErrNotConfigured) {
		return newProblem(http.StatusConflict, systemEmailNotConfiguredType, "System email not configured", "Save the system email settings before sending a test message.")
	}
	return err
}
