package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"inbok/internal/httpapi"
)

func TestExport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	if err := run([]string{"export", path}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want, err := httpapi.New().OpenAPI()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("exported file differs from the API document:\n%s", got)
	}
}

func TestRunUsage(t *testing.T) {
	cases := [][]string{nil, {"unknown"}, {"serve", "extra"}, {"export"}, {"export", "a", "b"}}
	for _, args := range cases {
		if err := run(args); !errors.Is(err, errUsage) {
			t.Fatalf("run(%q) = %v, want usage error", args, err)
		}
	}
}
