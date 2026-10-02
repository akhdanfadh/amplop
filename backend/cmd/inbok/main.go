package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	listenAddr = "127.0.0.1:8080"
	healthPath = "/api/v0/health"
)

type healthResponse struct {
	Data struct {
		Status string `json:"status"`
	} `json:"data"`
}

func main() {
	router := gin.New()
	router.HandleMethodNotAllowed = true

	// Report whether this process can answer HTTP requests.
	health := func(ctx *gin.Context) {
		response := healthResponse{}
		response.Data.Status = "ok"
		ctx.JSON(http.StatusOK, response)
	}

	router.GET(healthPath, health)
	router.HEAD(healthPath, health)

	server := http.Server{
		Addr:    listenAddr,
		Handler: router,
	}

	log.Printf("starting HTTP server on http://%s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
