package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
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
		Method:        http.MethodGet,
		Path:          healthPath,
		Summary:       "Check process health",
		DefaultStatus: http.StatusOK,
		Responses:     problemResponses(api),
	}, func(_ context.Context, _ *struct{}) (*output[healthStatus], error) {
		return respondData(healthStatus{Status: "ok"}), nil
	})
}
