package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every file carries the CSP, which allows exactly the shell's inline
// scripts. The hashes come from openssl, e.g.
// printf %s 'self.a=1' | openssl dgst -sha256 -binary | openssl base64.
func TestPolicy(t *testing.T) {
	want := "default-src 'self'; " +
		"script-src 'self' 'sha256-U+8MK+0uDzz63CVi5Lllj/7310ahz5UzrVluFdPYEWw=' " +
		"'sha256-FaFkoXjhIeCM3lyKXkAbhu1z9AUQTBId0sahMexEGEs='; " +
		"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	for _, path := range []string{"/security", "/_shell.html", "/assets/index-BrBO-kCw.js", "/robots.txt"} {
		t.Run(path, func(t *testing.T) {
			response := get(t, path)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, want, response.Header().Get("Content-Security-Policy"))
		})
	}
}

// A browser that keeps its copy updates the copy's headers from the 304, so
// the 304 carries the CSP too.
func TestPolicyRevalidate(t *testing.T) {
	first := get(t, "/security")
	request := httptest.NewRequest(http.MethodGet, "/security", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))

	again := serve(t, request)

	require.Equal(t, http.StatusNotModified, again.Code)
	assert.Equal(t, first.Header().Get("Content-Security-Policy"), again.Header().Get("Content-Security-Policy"))
}

// A script's hash is of its text as browsers read it: entities stay as
// written, a CRLF line break counts as LF, and a NUL byte, which a build's
// inline script can contain, counts as U+FFFD.
func TestPolicyScriptText(t *testing.T) {
	shell := "<!DOCTYPE html><script>a=\"&amp;\";\r\nb=2\x00</script>"

	got, err := newPolicy([]byte(shell))
	require.NoError(t, err)

	assert.Contains(t, got, "script-src 'self' 'sha256-+lHJxiwrxdbAHnjkrrZiXKv3yJRkrq5AJxEV4lR1zUI='; ")
}
