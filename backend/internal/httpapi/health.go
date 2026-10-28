package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/session"
)

// registerHealth adds the health operation.
func registerHealth(api huma.API) {
	registerGetHealth(api)
}

// ========================
// Health
// ========================

const healthPath = "/health"

type healthStatus struct {
	Status string `json:"status" enum:"ok"`
}

// registerGetHealth adds the process-health check.
func registerGetHealth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "getHealth",
		Metadata:      map[string]any{accessKey: session.AccessPublic},
		Method:        http.MethodGet,
		Path:          healthPath,
		Summary:       "Check process health",
		DefaultStatus: http.StatusOK,
		Responses:     healthResponses(api),
	}, handleGetHealth)
}

// handleGetHealth reports that this process can answer HTTP requests.
func handleGetHealth(_ context.Context, _ *struct{}) (*output[healthStatus], error) {
	return respondData(healthStatus{Status: "ok"}), nil
}

func healthResponses(api huma.API) map[string]*huma.Response {
	return problemResponses(api)
}
