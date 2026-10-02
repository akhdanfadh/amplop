package main

import (
	"errors"
	"log"
	"net/http"
	"os"

	"inbok/internal/httpapi"
)

const (
	listenAddr = "127.0.0.1:8080"
	exportPerm = 0o644 // new files: owner writes, everyone reads
)

var errUsage = errors.New("usage: inbok serve | inbok export <path>")

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// run executes one command. Example: inbok export openapi.json
func run(args []string) error {
	if len(args) == 0 {
		return errUsage
	}

	switch {
	case args[0] == "serve" && len(args) == 1:
		return serve()
	case args[0] == "export" && len(args) == 2:
		return export(args[1])
	}
	return errUsage
}

func serve() error {
	server := http.Server{
		Addr:    listenAddr,
		Handler: httpapi.New(listenAddr).Handler(),
	}

	log.Printf("starting HTTP server on http://%s", listenAddr)
	return server.ListenAndServe()
}

// export writes the OpenAPI document to path without starting a listener.
func export(path string) error {
	doc, err := httpapi.New(listenAddr).OpenAPI()
	if err != nil {
		return err
	}
	return os.WriteFile(path, doc, exportPerm)
}
