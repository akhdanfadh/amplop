// Package webapp holds the built web app that the inbok binary serves.
package webapp

import (
	"embed"
	"io/fs"
)

// embedded holds the build that make backend-build copies into dist.
//
// NOTE: Only dist/.gitkeep is committed, so a go build from a fresh
// checkout embeds no web app.
//
// all: keeps names starting with _ or ., e.g. _shell.html, which go:embed
// skips otherwise.
//
//go:embed all:dist
var embedded embed.FS

// Files returns the embedded build, rooted at the folder that holds
// _shell.html. Without a build it holds only the .gitkeep placeholder.
func Files() fs.FS {
	files, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Unreachable: fs.Sub fails only for an invalid path, and "dist" is valid.
		panic(err)
	}
	return files
}
