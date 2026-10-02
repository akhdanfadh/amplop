// Package httpapi registers inbok's HTTP operations.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

const apiPrefix = "/api/v0"

// API serves inbok's HTTP operations and describes them as OpenAPI.
type API struct {
	router *gin.Engine
	api    huma.API
}

// New registers every operation. addr appears in the OpenAPI server URL.
func New(addr string) *API {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	group := router.Group(apiPrefix)

	config := huma.DefaultConfig("inbok", "0.0.0")
	config.Servers = []*huma.Server{{URL: "http://" + addr + apiPrefix}}

	api := humagin.NewWithGroup(router, group, config)

	// Huma v2.39.1 omits the API prefix in nested JSON Schema references;
	// this is a workaround.
	router.GET("/schemas/:schema", func(ctx *gin.Context) {
		ctx.Redirect(http.StatusTemporaryRedirect, apiPrefix+ctx.Request.URL.Path)
	})

	registerHealth(api)

	return &API{router: router, api: api}
}

// Handler returns the handler for every registered route.
func (a *API) Handler() http.Handler {
	return a.router
}

// OpenAPI returns the document indented, so committed copies diff well.
func (a *API) OpenAPI() ([]byte, error) {
	doc, err := json.MarshalIndent(a.api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(doc, '\n'), nil
}
