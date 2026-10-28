package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/session"
)

const healthPath = "/health"

type healthStatus struct {
	Status string `json:"status" enum:"ok"`
}

// registerHealth adds the health operation.
func registerHealth(api huma.API) {
	// Report whether this process can answer HTTP requests.
	huma.Register(api, huma.Operation{
		OperationID:   "getHealth",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Method:        http.MethodGet,
		Path:          healthPath,
		Summary:       "Check process health",
		DefaultStatus: http.StatusOK,
		Responses:     problemResponses(api),
	}, func(_ context.Context, _ *struct{}) (*output[healthStatus], error) {
		return respondData(healthStatus{Status: "ok"}), nil
	})
}
