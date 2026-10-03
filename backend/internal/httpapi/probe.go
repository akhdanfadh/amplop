package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/probe"
)

const probeEventsPath = "/probe/events"

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

// probeService is what the probe operation calls; [probe.Service] in
// production. Tests answer with fixed results, so they check only the HTTP
// mapping, not the probe's rules.
type probeService interface {
	Add(ctx context.Context, entry probe.Entry) (probe.Result, error)
}

// registerProbe adds the probe operation.
func registerProbe(api huma.API, service probeService) {
	// Add an amount to the shared counter and record it, in one transaction.
	huma.Register(api, huma.Operation{
		OperationID:   "createProbeEvent",
		Method:        http.MethodPost,
		Path:          probeEventsPath,
		Summary:       "Add to the probe counter",
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict},
	}, func(ctx context.Context, in *struct{ Body newProbeEvent }) (*output[probeEventCreated], error) {
		result, err := service.Add(ctx, probe.Entry{
			Note:        in.Body.Note,
			Amount:      in.Body.Amount,
			Highlighted: in.Body.Highlighted,
			Tag:         in.Body.Tag,
		})

		// Domain errors become fixed messages; any other error is a 500
		// whose text is logged, never sent.
		if errors.Is(err, probe.ErrCounterFull) {
			return nil, huma.Error409Conflict("counter is full")
		}
		if err != nil {
			return nil, err
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
	})
}
