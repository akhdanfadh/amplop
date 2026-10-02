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

type healthHead struct {
	ContentType string `header:"Content-Type"`
}

// registerHealth adds the health GET and HEAD operations.
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

	// HEAD returns GET's status and media type without a response body.
	huma.Register(api, huma.Operation{
		OperationID:   "headHealth",
		Method:        http.MethodHead,
		Path:          healthPath,
		Summary:       "Check process health headers",
		DefaultStatus: http.StatusOK,
	}, func(_ context.Context, _ *struct{}) (*healthHead, error) {
		return &healthHead{ContentType: "application/json"}, nil
	})
}
