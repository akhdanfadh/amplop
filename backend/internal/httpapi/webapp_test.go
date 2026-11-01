package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testShell = "<!DOCTYPE html><title>inbok</title>"

// webAppFiles is a small build: the shell, a hashed script and font, a file
// of a kind no MIME table knows, an unhashed file, and the placeholder
// the embed folder keeps.
func webAppFiles() fstest.MapFS {
	return fstest.MapFS{
		"_shell.html":                    {Data: []byte(testShell)},
		"assets/index-BrBO-kCw.js":       {Data: []byte("console.log(1)")},
		"assets/geist-DC-KSUi6.woff2":    {Data: []byte("wOF2font")},
		"assets/data-Dx91kQ2a.inbokdata": {Data: []byte{0, 1}},
		"robots.txt":                     {Data: []byte("User-agent: *")},
		".gitkeep":                       {Data: []byte{}},
	}
}

// newWebAppHandler returns the API's handler after serving webAppFiles.
func newWebAppHandler(t *testing.T) http.Handler {
	t.Helper()
	a := New(discard, Services{})
	require.NoError(t, a.ServeWebApp(webAppFiles()))
	return a.Handler()
}

// Browser routes have no file, so they receive the shell.
func TestWebAppShell(t *testing.T) {
	for _, path := range []string{"/", "/security", "/sign-in", "/a/b"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "text/html; charset=utf-8", response.Header().Get("Content-Type"))
			assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
			assert.Equal(t, testShell, response.Body.String())
		})
	}
}

// Files are sent as built. Hashed files under /assets/ never change, so
// browsers keep them; other files are checked again before each use.
func TestWebAppFiles(t *testing.T) {
	cases := []struct {
		path, contentType, cacheControl, body string
	}{
		{"/assets/index-BrBO-kCw.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", "console.log(1)"},
		{"/assets/geist-DC-KSUi6.woff2", "font/woff2", "public, max-age=31536000, immutable", "wOF2font"},
		{"/assets/data-Dx91kQ2a.inbokdata", "application/octet-stream", "public, max-age=31536000, immutable", "\x00\x01"},
		{"/robots.txt", "text/plain; charset=utf-8", "no-cache", "User-agent: *"},
		{"/_shell.html", "text/html; charset=utf-8", "no-cache", testShell},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, c.path, nil))

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, c.contentType, response.Header().Get("Content-Type"))
			assert.Equal(t, c.cacheControl, response.Header().Get("Cache-Control"))
			assert.Equal(t, c.body, response.Body.String())
		})
	}
}

// A missing file, a hidden file, and every /api path answer the API's 404,
// never the shell: a browser asking for a script must not receive HTML.
func TestWebAppNotFound(t *testing.T) {
	for _, path := range []string{
		"/assets/security-OLD.js",
		"/assets/",
		"/assets",
		"/favicon.ico",
		"/index.html",
		"/.gitkeep",
		"/api",
		"/api/",
		"/api/v0/nope",
		"/api/v1/health",
		"//api/v0/health",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

			require.Equal(t, http.StatusNotFound, response.Code)
			assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			assert.NotContains(t, response.Body.String(), testShell)
		})
	}
}

// Only /api and paths under /api/ belong to the API.
func TestWebAppNotAPI(t *testing.T) {
	response := httptest.NewRecorder()
	newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/apiary", nil))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, testShell, response.Body.String())
}

// The API's own routes answer as before.
func TestWebAppAPI(t *testing.T) {
	response := httptest.NewRecorder()
	newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v0/health", nil))

	require.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"data": {"status": "ok"}}`, response.Body.String())
}

// The web app's paths only answer reads; the API's paths answer as before.
func TestWebAppMethods(t *testing.T) {
	cases := []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodPost, "/security", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPut, "/assets/index-BrBO-kCw.js", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodDelete, "/favicon.ico", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodOptions, "/", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPost, "/api/v0/nope", http.StatusNotFound, ""},
		{http.MethodDelete, "/api/v0/health", http.StatusMethodNotAllowed, "GET"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			request := httptest.NewRequest(c.method, c.path, nil)
			// Same origin, so the cross-origin check lets the write reach routing.
			request.Header.Set("Origin", "http://"+request.Host)
			response := httptest.NewRecorder()
			newWebAppHandler(t).ServeHTTP(response, request)

			require.Equal(t, c.status, response.Code)
			assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
			assert.Equal(t, c.allow, response.Header().Get("Allow"))
		})
	}
}

// A write without a browser's source headers is refused before routing, as
// on the API.
func TestWebAppWriteWithoutSource(t *testing.T) {
	response := httptest.NewRecorder()
	newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/security", nil))

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
}

// HEAD sends a GET's headers without its body.
func TestWebAppHead(t *testing.T) {
	response := httptest.NewRecorder()
	newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/security", nil))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "text/html; charset=utf-8", response.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
	assert.Equal(t, strconv.Itoa(len(testShell)), response.Header().Get("Content-Length"))
	assert.Empty(t, response.Body.String())
}

// A Range header is ignored: the files are small, and a partial file has no
// use here.
func TestWebAppRange(t *testing.T) {
	for _, value := range []string{"bytes=0-3", "bytes=500-600"} {
		t.Run(value, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/security", nil)
			request.Header.Set("Range", value)
			response := httptest.NewRecorder()

			newWebAppHandler(t).ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, testShell, response.Body.String())
		})
	}
}

// Only a build file's exact path serves it: a request that climbs out of
// the build or rewrites a path gets the API's 404 or the shell, never
// another file.
func TestWebAppTraversal(t *testing.T) {
	cases := []struct {
		target string
		status int
	}{
		{"/assets/../_shell.html", http.StatusNotFound},
		{"/assets/%2e%2e/robots.txt", http.StatusNotFound},
		{"/..%2f..%2fgo.mod", http.StatusNotFound},
		{"/..\\..\\go.mod", http.StatusNotFound},
		{"/assets//index-BrBO-kCw.js", http.StatusNotFound},
		{"/ASSETS/index-BrBO-kCw.js", http.StatusNotFound},
		{"/robots.txt/", http.StatusOK},
		{"/../../etc/passwd", http.StatusNotFound},
		{"/%2e%2e/%2e%2e/etc/passwd", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			response := httptest.NewRecorder()
			newWebAppHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, c.target, nil))

			require.Equal(t, c.status, response.Code)
			if c.status == http.StatusOK {
				assert.Equal(t, testShell, response.Body.String())
			}
		})
	}
}

// A browser rechecking an unchanged file receives no body.
func TestWebAppRevalidate(t *testing.T) {
	// Given the shell's tag from a first request.
	handler := newWebAppHandler(t)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/security", nil))
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)

	for _, header := range []string{etag, `"other", W/` + etag, "*"} {
		t.Run(header, func(t *testing.T) {
			// When the browser asks again with that tag.
			request := httptest.NewRequest(http.MethodGet, "/sign-in", nil)
			request.Header.Set("If-None-Match", header)
			again := httptest.NewRecorder()
			handler.ServeHTTP(again, request)

			// Then it keeps its copy, still told how to cache it.
			assert.Equal(t, http.StatusNotModified, again.Code)
			assert.Empty(t, again.Body.String())
			assert.Equal(t, etag, again.Header().Get("ETag"))
			assert.Equal(t, "no-cache", again.Header().Get("Cache-Control"))
		})
	}
}

// A tag for other contents sends the file.
func TestWebAppChanged(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/security", nil)
	request.Header.Set("If-None-Match", `"old"`)
	response := httptest.NewRecorder()

	newWebAppHandler(t).ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, testShell, response.Body.String())
}

// Files with different contents have different tags.
func TestWebAppETag(t *testing.T) {
	handler := newWebAppHandler(t)
	tags := map[string]bool{}
	for _, path := range []string{"/", "/robots.txt", "/assets/index-BrBO-kCw.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		tags[response.Header().Get("ETag")] = true
	}

	assert.Len(t, tags, 3)
	assert.NotContains(t, tags, "")
}

// Files without a build leave every path outside the API at the API's 404.
func TestWebAppNotBuilt(t *testing.T) {
	a := New(discard, Services{})

	err := a.ServeWebApp(fstest.MapFS{".gitkeep": {Data: []byte{}}})
	require.ErrorIs(t, err, ErrNoWebApp)

	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/security", nil))

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, problemContentType, response.Header().Get("Content-Type"))
}
