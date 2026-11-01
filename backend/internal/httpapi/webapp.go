package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// webAppShell is the page every browser route starts from.
	webAppShell = "_shell.html"
	// webAppAssets is the folder of the build's hashed files.
	webAppAssets = "/assets/"

	// A new build gives a changed asset a new name, so a stored copy never
	// goes stale.
	cacheForever = "public, max-age=31536000, immutable"
	// Browsers may store the file but must ask again before each use.
	cacheRevalidate = "no-cache"
)

// webAppTypes maps the extensions the web build writes that Go's own
// table lacks to their content type.
//
// NOTE: Without it, a .woff2 font's type would come from the host's MIME
// files, which a minimal container image may not have.
var webAppTypes = map[string]string{
	".woff2": "font/woff2",
}

// ErrNoWebApp reports that the files given to [API.ServeWebApp] are not a
// web app build: they have no shell.
var ErrNoWebApp = errors.New("no web app build: missing " + webAppShell)

// webFile is one built file, ready to send.
type webFile struct {
	data         []byte
	contentType  string
	cacheControl string
	// etag lets a browser recheck a no-cache file without downloading it.
	etag string
}

// webApp serves the built web app.
type webApp struct {
	// files maps each URL path, e.g. /assets/index-BrBO-kCw.js, to its file.
	files map[string]webFile
	shell webFile
}

// ServeWebApp serves files, a web app build, on every path outside /api.
// Call before serving requests. Returns [ErrNoWebApp] if files has no
// shell, or the error from reading files.
func (a *API) ServeWebApp(files fs.FS) error {
	app := &webApp{files: map[string]webFile{}}
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Hidden files, e.g. the embed folder's .gitkeep, are not part of
		// the build.
		if name != "." && isHidden(name) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		app.files["/"+name] = newWebFile(name, data)
		return nil
	})
	if err != nil {
		return err
	}

	shell, ok := app.files["/"+webAppShell]
	if !ok {
		return ErrNoWebApp
	}
	app.shell = shell
	a.router.NoRoute(app.handleWebApp)
	return nil
}

// newWebFile returns the file at name, relative to the build, with data.
func newWebFile(name string, data []byte) webFile {
	contentType, ok := webAppTypes[path.Ext(name)]
	if !ok {
		contentType = mime.TypeByExtension(path.Ext(name))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	cacheControl := cacheRevalidate
	if strings.HasPrefix("/"+name, webAppAssets) {
		cacheControl = cacheForever
	}
	sum := sha256.Sum256(data)
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
	return webFile{data: data, contentType: contentType, cacheControl: cacheControl, etag: etag}
}

// handleWebApp answers a request no API route matched: a built file, the
// shell for a browser route, or the API's 404 or 405.
func (app *webApp) handleWebApp(ctx *gin.Context) {
	urlPath := ctx.Request.URL.Path
	if isAPIPath(urlPath) {
		handleNoRoute(ctx)
		return
	}
	if ctx.Request.Method != http.MethodGet && ctx.Request.Method != http.MethodHead {
		ctx.Header("Allow", "GET, HEAD")
		writeProblem(ctx, requestProblem(http.StatusMethodNotAllowed))
		return
	}

	file, ok := app.files[urlPath]
	if !ok && !isBrowserRoute(urlPath) {
		handleNoRoute(ctx)
		return
	}
	if !ok {
		file = app.shell
	}
	ctx.Header("Cache-Control", file.cacheControl)
	ctx.Header("ETag", file.etag)
	if etagMatches(ctx.GetHeader("If-None-Match"), file.etag) {
		ctx.Status(http.StatusNotModified)
		return
	}
	ctx.Header("Content-Type", file.contentType)
	// Set before writing so HEAD, which sends no body, reports it too.
	ctx.Header("Content-Length", strconv.Itoa(len(file.data)))
	ctx.Status(http.StatusOK)
	if ctx.Request.Method == http.MethodHead {
		return
	}
	_, _ = ctx.Writer.Write(file.data)
}

// isAPIPath reports whether urlPath is /api or under /api/.
func isAPIPath(urlPath string) bool {
	// Gin routes //api/v0/health to no API route; it is still not a page.
	urlPath = "/" + strings.TrimLeft(urlPath, "/")
	// Later versions, e.g. /api/v1, belong to the API too.
	return urlPath == "/api" || strings.HasPrefix(urlPath, "/api/")
}

// isBrowserRoute reports whether urlPath, which has no built file, can be a
// route of the browser's router.
func isBrowserRoute(urlPath string) bool {
	switch {
	// A missing asset is an old or mistyped build file, not a page.
	case urlPath+"/" == webAppAssets, strings.HasPrefix(urlPath, webAppAssets):
		return false
	// Browser routes have no extension; /favicon.ico is a missing file.
	case path.Ext(urlPath) != "":
		return false
	case isHidden(urlPath):
		return false
	}
	return true
}

// isHidden reports whether any segment of name starts with a dot.
func isHidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.Contains(name, "/.")
}

// etagMatches reports whether an If-None-Match header value names etag,
// e.g. `"abc", W/"abc"`, or is *.
func etagMatches(header, etag string) bool {
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(header, ",") {
		// If-None-Match compares weakly: W/"abc" matches "abc".
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == etag {
			return true
		}
	}
	return false
}
