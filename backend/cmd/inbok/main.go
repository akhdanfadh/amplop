package main

import (
	"encoding/json"
	"log"
	"net/http"
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
	// Report whether this process can answer HTTP requests.
	mux := http.NewServeMux()
	mux.HandleFunc(http.MethodGet+" "+healthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		response := healthResponse{}
		response.Data.Status = "ok"

		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("write health response: %v", err)
		}
	})

	server := http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	log.Printf("starting HTTP server on http://%s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
