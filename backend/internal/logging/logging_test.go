package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A line logged with a request's context carries its ID; others do not.
func TestRequestID(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out)

	logger.InfoContext(WithRequestID(t.Context(), "req-1"), "with request")
	logger.InfoContext(t.Context(), "without request")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "request_id=req-1")
	assert.NotContains(t, lines[1], "request_id")
}

// Loggers derived with attributes or groups keep adding the ID, once. Inside
// a group it lands in the group, as slog places every record attribute.
func TestRequestIDDerived(t *testing.T) {
	var out bytes.Buffer
	ctx := WithRequestID(t.Context(), "req-2")

	New(&out).With("feature", "mail").WithGroup("send").InfoContext(ctx, "derived", "step", "dial")

	assert.Contains(t, out.String(), "feature=mail")
	assert.Contains(t, out.String(), "send.request_id=req-2")
	assert.Equal(t, 1, strings.Count(out.String(), "request_id="))
}

// New writes Info and above, as slog's text handler does by default.
func TestNewLevel(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out)

	logger.Debug("hidden")
	logger.Info("shown")

	assert.NotContains(t, out.String(), "hidden")
	assert.Contains(t, out.String(), "level=INFO msg=shown")
}

// Handler wraps any handler, e.g. JSON.
func TestHandler(t *testing.T) {
	var out bytes.Buffer

	slog.New(Handler(slog.NewJSONHandler(&out, nil))).InfoContext(WithRequestID(t.Context(), "req-3"), "json")

	assert.Contains(t, out.String(), `"request_id":"req-3"`)
}

// A left-out key disappears from lines, whether logged with the line or
// added to the logger before; other attributes stay.
func TestWithoutAttrs(t *testing.T) {
	var out bytes.Buffer
	logger := WithoutAttrs(New(&out), "database_url")

	logger.Info("connect", "database_url", "postgres://u:secret@h/db", "schema", "dbos")
	logger.With("database_url", "postgres://u:secret@h/db").WithGroup("g").Info("derived", "database_url", "x")

	assert.NotContains(t, out.String(), "secret")
	assert.NotContains(t, out.String(), "database_url")
	assert.Contains(t, out.String(), "msg=connect schema=dbos")
	assert.Contains(t, out.String(), "msg=derived")
}
