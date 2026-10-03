package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

const healthPath = "/health"

type healthResponse struct {
	Data struct {
		Status string `json:"status" enum:"ok"`
	} `json:"data"`
}

type healthOutput struct {
	Body healthResponse
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
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		output := &healthOutput{}
		output.Body.Data.Status = "ok"
		return output, nil
	})
}
