package webapp

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

const (
	// shell is the page every browser route starts from.
	shell = "_shell.html"
	// assets is the folder of the build's hashed files.
	assets = "/assets/"

	// A new build gives a changed asset a new name, so a stored copy never
	// goes stale.
	cacheForever = "public, max-age=31536000, immutable"
	// Browsers may store the file but must ask again before each use.
	cacheRevalidate = "no-cache"
)

// contentTypes maps the extensions the web build writes that Go's own table
// lacks to their content type.
//
// NOTE: Without it, a .woff2 font's type would come from the host's MIME
// files, which a minimal container image may not have.
var contentTypes = map[string]string{
	".woff2": "font/woff2",
}

// ErrNoBuild reports that the files given to [Load] are not a build: they
// have no shell.
var ErrNoBuild = errors.New("no web app build: missing " + shell)

// file is one built file, ready to send.
type file struct {
	data         []byte
	contentType  string
	cacheControl string
	// etag lets a browser recheck a no-cache file without downloading it.
	etag string
}

// Build is a loaded web app build, ready to serve.
type Build struct {
	// files maps each URL path, e.g. /assets/index-BrBO-kCw.js, to its file.
	files map[string]file
	shell file
	// policy is the Content-Security-Policy sent with every file.
	policy string
}

// Load reads every file of files, a web app build, into memory. Returns
// [ErrNoBuild] if files has no shell, or the error from reading files.
func Load(files fs.FS) (*Build, error) {
	build := &Build{files: map[string]file{}}
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
		build.files["/"+name] = newFile(name, data)
		return nil
	})
	if err != nil {
		return nil, err
	}

	shellFile, ok := build.files["/"+shell]
	if !ok {
		return nil, ErrNoBuild
	}
	build.shell = shellFile
	build.policy, err = newPolicy(shellFile.data)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", shell, err)
	}
	return build, nil
}

// newFile returns the file at name, relative to the build, with data.
func newFile(name string, data []byte) file {
	contentType, ok := contentTypes[path.Ext(name)]
	if !ok {
		contentType = mime.TypeByExtension(path.Ext(name))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	cacheControl := cacheRevalidate
	if strings.HasPrefix("/"+name, assets) {
		cacheControl = cacheForever
	}
	sum := sha256.Sum256(data)
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
	return file{data: data, contentType: contentType, cacheControl: cacheControl, etag: etag}
}

// Serve answers request, a GET or HEAD, with the built file at its path or,
// for a browser route, the shell. Returns false, having written nothing, if
// the build has neither for the path, e.g. a missing /assets/ file.
func (b *Build) Serve(w http.ResponseWriter, request *http.Request) bool {
	urlPath := request.URL.Path
	served, ok := b.files[urlPath]
	if !ok && !isBrowserRoute(urlPath) {
		return false
	}
	if !ok {
		served = b.shell
	}

	header := w.Header()
	header.Set("Content-Security-Policy", b.policy)
	header.Set("Cache-Control", served.cacheControl)
	header.Set("ETag", served.etag)
	if etagMatches(request.Header.Get("If-None-Match"), served.etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	header.Set("Content-Type", served.contentType)
	// Set before writing so HEAD, which sends no body, reports it too.
	header.Set("Content-Length", strconv.Itoa(len(served.data)))
	w.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = w.Write(served.data)
	}
	return true
}

// isBrowserRoute reports whether urlPath, which has no built file, can be a
// route of the browser's router.
func isBrowserRoute(urlPath string) bool {
	switch {
	// A missing asset is an old or mistyped build file, not a page.
	case urlPath+"/" == assets, strings.HasPrefix(urlPath, assets):
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
