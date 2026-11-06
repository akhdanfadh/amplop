package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/probe"
	"inbok/internal/session"
)

// probeService is what the probe operation calls; [probe.Service] in
// production. Tests answer with fixed results, so they check only the HTTP
// mapping, not the probe's rules.
type probeService interface {
	Add(ctx context.Context, entry probe.Entry) (probe.Result, error)
}

type probeHandler struct {
	service probeService
}

// registerProbe adds the probe operation.
func registerProbe(api huma.API, service probeService) {
	registerProbeEvent(api, probeHandler{service: service})
}

// ========================
// Create probe event
// ========================

const (
	probeEventsPath = "/probe/events"
	counterFullType = problemPrefix + "counter-full"
)

// newProbeEvent is the request body. Its tags are the contract: Huma
// rejects a body that breaks them with a 422 naming the field, e.g.
// body.amount, and the generated client checks the same rules.
type newProbeEvent struct {
	Note        string  `json:"note" maxLength:"120" pattern:"\\S" patternDescription:"not blank"`
	Amount      int     `json:"amount" minimum:"1" maximum:"10"`
	Highlighted bool    `json:"highlighted"`
	Tag         *string `json:"tag,omitempty" nullable:"true" minLength:"1" maxLength:"40" doc:"Omit or null for no tag."`
}

// probeEvent is a saved event. Tag is always sent, null for no tag.
type probeEvent struct {
	ID          int64     `json:"id"`
	Note        string    `json:"note"`
	Amount      int       `json:"amount"`
	Highlighted bool      `json:"highlighted"`
	Tag         *string   `json:"tag" nullable:"true"`
	CreatedAt   time.Time `json:"createdAt"`
}

// probeEventCreated is the saved event and the counter's new total.
type probeEventCreated struct {
	Event probeEvent `json:"event"`
	Total int64      `json:"total"`
}

// registerProbeEvent adds the counter-write operation.
func registerProbeEvent(api huma.API, handler probeHandler) {
	huma.Register(api, huma.Operation{
		OperationID:   "createProbeEvent",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Method:        http.MethodPost,
		Path:          probeEventsPath,
		Summary:       "Add Probe Event",
		Tags:          []string{tagProbe},
		Description:   "Adds the entry's amount to the shared counter and records the entry.",
		DefaultStatus: http.StatusCreated,
		Responses:     probeEventResponses(api),
	}, handler.handleCreateProbeEvent)
}

// handleCreateProbeEvent maps the saved event and total to the HTTP response.
func (h probeHandler) handleCreateProbeEvent(ctx context.Context, in *struct{ Body newProbeEvent }) (*output[probeEventCreated], error) {
	result, err := h.service.Add(ctx, probe.Entry{
		Note:        in.Body.Note,
		Amount:      in.Body.Amount,
		Highlighted: in.Body.Highlighted,
		Tag:         in.Body.Tag,
	})

	// Domain errors become fixed messages; any other error is a 500
	// whose text is logged, never sent.
	if err != nil {
		return nil, probeEventProblem(err)
	}

	event := result.Event
	return respondData(probeEventCreated{
		Event: probeEvent{
			ID:          event.ID,
			Note:        event.Note,
			Amount:      event.Amount,
			Highlighted: event.Highlighted,
			Tag:         event.Tag,
			CreatedAt:   event.CreatedAt,
		},
		Total: result.Total,
	}), nil
}

func probeEventResponses(api huma.API) map[string]*huma.Response {
	responses := problemResponses(api, http.StatusConflict)
	conflict := responses[strconv.Itoa(http.StatusConflict)]
	conflict.Description = "The counter is full."
	conflict.Content[problemContentType].Example = probeEventProblem(probe.ErrCounterFull)

	return responses
}

// probeEventProblem maps counter-limit failures to safe public text.
func probeEventProblem(err error) error {
	if errors.Is(err, probe.ErrCounterFull) {
		return newProblem(http.StatusConflict, counterFullType, "Counter full", "The counter is full.")
	}

	return err
}
