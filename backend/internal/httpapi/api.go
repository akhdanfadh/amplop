// Package httpapi registers inbok's HTTP operations.
package httpapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

const apiPrefix = "/api/v0"

// New returns the HTTP handler and its API description for addr.
func New(addr string) (http.Handler, huma.API) {
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

	return router, api
}
