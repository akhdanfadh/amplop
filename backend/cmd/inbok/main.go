package main

import (
	"errors"
	"log"
	"net/http"
	"os"

	"inbok/internal/config"
	"inbok/internal/httpapi"
)

const exportPerm = 0o644 // new files: owner writes, everyone reads

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
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	server := http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: httpapi.New().Handler(),
	}

	log.Printf("starting HTTP server on http://%s", cfg.HTTPAddr)
	return server.ListenAndServe()
}

// export writes the OpenAPI document to path without starting a listener.
func export(path string) error {
	doc, err := httpapi.New().OpenAPI()
	if err != nil {
		return err
	}
	return os.WriteFile(path, doc, exportPerm)
}
