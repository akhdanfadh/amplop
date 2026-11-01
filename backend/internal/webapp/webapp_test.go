package webapp

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The embedded files hold the shell exactly when the folder on disk does.
func TestFiles(t *testing.T) {
	_, err := os.Stat("dist/_shell.html")
	if !errors.Is(err, fs.ErrNotExist) {
		require.NoError(t, err)
	}
	onDisk := err == nil

	_, err = fs.Stat(Files(), "_shell.html")

	assert.Equal(t, onDisk, err == nil, "the embedded files must match the folder on disk")
}

// Files is rooted at the build folder, where the placeholder always is.
func TestFilesRoot(t *testing.T) {
	_, err := fs.Stat(Files(), ".gitkeep")

	assert.NoError(t, err)
}
