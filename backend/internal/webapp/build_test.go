package webapp

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testShell has two inline scripts, like the real shell, and a script file.
const testShell = `<!DOCTYPE html><title>amplop</title><script>self.a=1</script>` +
	`<script type="module" async src="/assets/index-BrBO-kCw.js"></script>` +
	`<script>document.currentScript.remove()</script>`

// testFiles is a small build: the shell, a hashed script and stylesheet, a
// file of a kind no MIME table knows, an unhashed file, and the placeholder
// the embed folder keeps.
func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"_shell.html":                     {Data: []byte(testShell)},
		"assets/index-BrBO-kCw.js":        {Data: []byte("console.log(1)")},
		"assets/styles-BRqUNFMl.css":      {Data: []byte("body{}")},
		"assets/data-Dx91kQ2a.amplopdata": {Data: []byte{0, 1}},
		"robots.txt":                      {Data: []byte("User-agent: *")},
		".gitkeep":                        {Data: []byte{}},
	}
}

// serve sends request to a [Build] of [testFiles]. A path the build has
// nothing for answers 404, as the API does.
func serve(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	build, err := Load(testFiles())
	require.NoError(t, err)
	response := httptest.NewRecorder()
	if !build.Serve(response, request) {
		response.WriteHeader(http.StatusNotFound)
	}
	return response
}

// get sends a GET for target to a [Build] of [testFiles].
func get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, httptest.NewRequest(http.MethodGet, target, nil))
}

// Browser routes have no file, so they receive the shell.
func TestBuildShell(t *testing.T) {
	for _, path := range []string{"/", "/security", "/sign-in", "/a/b", "/apiary"} {
		t.Run(path, func(t *testing.T) {
			response := get(t, path)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "text/html; charset=utf-8", response.Header().Get("Content-Type"))
			assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
			assert.Equal(t, testShell, response.Body.String())
		})
	}
}

// Files are sent as built. Hashed files under /assets/ never change, so
// browsers keep them; other files are checked again before each use.
func TestBuildFiles(t *testing.T) {
	cases := []struct {
		path, contentType, cacheControl, body string
	}{
		{"/assets/index-BrBO-kCw.js", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", "console.log(1)"},
		{"/assets/styles-BRqUNFMl.css", "text/css; charset=utf-8", "public, max-age=31536000, immutable", "body{}"},
		{"/assets/data-Dx91kQ2a.amplopdata", "application/octet-stream", "public, max-age=31536000, immutable", "\x00\x01"},
		{"/robots.txt", "text/plain; charset=utf-8", "no-cache", "User-agent: *"},
		{"/_shell.html", "text/html; charset=utf-8", "no-cache", testShell},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			response := get(t, c.path)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, c.contentType, response.Header().Get("Content-Type"))
			assert.Equal(t, c.cacheControl, response.Header().Get("Cache-Control"))
			assert.Equal(t, c.body, response.Body.String())
		})
	}
}

// A missing file and a hidden file get nothing, never the shell: a browser
// asking for a script must not receive HTML.
func TestBuildNotFound(t *testing.T) {
	for _, path := range []string{
		"/assets/security-OLD.js",
		"/assets/",
		"/assets",
		"/favicon.ico",
		"/index.html",
		"/.gitkeep",
	} {
		t.Run(path, func(t *testing.T) {
			response := get(t, path)

			require.Equal(t, http.StatusNotFound, response.Code)
			assert.Empty(t, response.Header())
			assert.Empty(t, response.Body.String())
		})
	}
}

// Only a build file's exact path serves it: a request that climbs out of
// the build or rewrites a path gets nothing or the shell, never another
// file.
func TestBuildTraversal(t *testing.T) {
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
		{"/../../etc/passwd", http.StatusNotFound},
		{"/%2e%2e/%2e%2e/etc/passwd", http.StatusNotFound},
		{"/robots.txt/", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			response := get(t, c.target)

			require.Equal(t, c.status, response.Code)
			if c.status == http.StatusOK {
				assert.Equal(t, testShell, response.Body.String())
			}
		})
	}
}

// HEAD sends a GET's headers without its body.
func TestBuildHead(t *testing.T) {
	response := serve(t, httptest.NewRequest(http.MethodHead, "/security", nil))

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "text/html; charset=utf-8", response.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
	assert.Equal(t, strconv.Itoa(len(testShell)), response.Header().Get("Content-Length"))
	assert.Empty(t, response.Body.String())
}

// A Range header is ignored: the files are small, and a partial file has no
// use here.
func TestBuildRange(t *testing.T) {
	for _, value := range []string{"bytes=0-3", "bytes=500-600"} {
		t.Run(value, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/security", nil)
			request.Header.Set("Range", value)

			response := serve(t, request)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, testShell, response.Body.String())
		})
	}
}

// A browser rechecking an unchanged file receives no body.
func TestBuildRevalidate(t *testing.T) {
	etag := get(t, "/security").Header().Get("ETag")
	require.NotEmpty(t, etag)

	for _, header := range []string{etag, `"other", W/` + etag, "*"} {
		t.Run(header, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/sign-in", nil)
			request.Header.Set("If-None-Match", header)

			response := serve(t, request)

			// It keeps its copy, still told how to cache it.
			assert.Equal(t, http.StatusNotModified, response.Code)
			assert.Empty(t, response.Body.String())
			assert.Equal(t, etag, response.Header().Get("ETag"))
			assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
		})
	}
}

// A tag for other contents sends the file.
func TestBuildChanged(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/security", nil)
	request.Header.Set("If-None-Match", `"old"`)

	response := serve(t, request)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, testShell, response.Body.String())
}

// Files with different contents have different tags.
func TestBuildETag(t *testing.T) {
	tags := map[string]bool{}
	for _, path := range []string{"/", "/robots.txt", "/assets/index-BrBO-kCw.js"} {
		tags[get(t, path).Header().Get("ETag")] = true
	}

	assert.Len(t, tags, 3)
	assert.NotContains(t, tags, "")
}

// Files without a shell are not a build.
func TestBuildNotBuilt(t *testing.T) {
	_, err := Load(fstest.MapFS{".gitkeep": {Data: []byte{}}})

	require.ErrorIs(t, err, ErrNoBuild)
}
