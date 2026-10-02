package main

import (
	"log"
	"net/http"

	"inbok/internal/httpapi"
)

const listenAddr = "127.0.0.1:8080"

func main() {
	handler, _ := httpapi.New(listenAddr)

	server := http.Server{
		Addr:    listenAddr,
		Handler: handler,
	}

	log.Printf("starting HTTP server on http://%s", listenAddr)
	log.Fatal(server.ListenAndServe())
}
