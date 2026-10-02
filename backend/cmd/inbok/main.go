package main

import (
	"context"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

const (
	listenAddr = "127.0.0.1:8080"
	apiPrefix  = "/api/v0"
	healthPath = "/health"
)

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

func newAPI() (*gin.Engine, huma.API) {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	group := router.Group(apiPrefix)

	config := huma.DefaultConfig("inbok", "0.0.0")
	config.Servers = []*huma.Server{{URL: "http://" + listenAddr + apiPrefix}}

	api := humagin.NewWithGroup(router, group, config)

	// Huma v2.39.1 omits the API prefix in nested JSON Schema references;
	// this is a workaround.
	router.GET("/schemas/:schema", func(ctx *gin.Context) {
		ctx.Redirect(http.StatusTemporaryRedirect, apiPrefix+ctx.Request.URL.Path)
	})

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

	return router, api
}

func main() {
	router, _ := newAPI()

	server := http.Server{
		Addr:    listenAddr,
		Handler: router,
	}

	log.Printf("starting HTTP server on http://%s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
