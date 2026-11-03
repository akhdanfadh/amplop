package passwordreset

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/systememail"
)

type contextKey struct{}

// logBuffer returns a logger that writes text lines to the returned buffer.
func logBuffer() (*slog.Logger, *bytes.Buffer) {
	var out bytes.Buffer
	return slog.New(slog.NewTextHandler(&out, nil)), &out
}

// Work outlives its request: it keeps the request's values, not its
// cancellation, and gets its own deadline.
func TestBackgroundContext(t *testing.T) {
	background := NewBackground(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "request 7"))
	var value any
	var workErr error
	var deadline time.Time

	background.Go(ctx, "test", func(ctx context.Context) error {
		cancel()
		value, workErr = ctx.Value(contextKey{}), ctx.Err()
		deadline, _ = ctx.Deadline()
		return nil
	})
	require.NoError(t, background.Wait(t.Context()))

	assert.Equal(t, "request 7", value)
	require.NoError(t, workErr, "the request's end does not end the work")
	assert.WithinDuration(t, time.Now().Add(backgroundTimeout), deadline, time.Second)
}

// A failure is logged with its purpose; a failed send only with its reason,
// since the server's reply can quote the message.
func TestBackgroundFailure(t *testing.T) {
	logger, out := logBuffer()
	background := NewBackground(logger)

	background.Go(t.Context(), "first", func(context.Context) error { return errors.New("database down") })
	background.Go(t.Context(), "second", func(context.Context) error {
		return &systememail.SendError{Reason: systememail.ReasonRecipient, Err: errors.New("550 rejected: secret-in-body")}
	})
	require.NoError(t, background.Wait(t.Context()))

	assert.Contains(t, out.String(), `level=ERROR msg="password reset work failed" purpose=first err="database down"`)
	assert.Contains(t, out.String(), `level=WARN msg="password reset email not sent" purpose=second reason=recipient`)
	assert.NotContains(t, out.String(), "secret-in-body")
}

// A panic in the work is logged with its stack instead of ending the
// server; its value, which could hold a server's reply, is not.
func TestBackgroundPanic(t *testing.T) {
	logger, out := logBuffer()
	background := NewBackground(logger)

	background.Go(t.Context(), "test", func(context.Context) error { panic("secret-in-reply") })
	require.NoError(t, background.Wait(t.Context()))

	assert.Contains(t, out.String(), `msg="password reset work panicked" purpose=test stack=`)
	assert.Contains(t, out.String(), "background_test.go")
	assert.NotContains(t, out.String(), "secret-in-reply")
}

// Work may start while Wait runs, as it can when shutdown times out with
// requests still running; Wait still returns once all of it has finished.
func TestBackgroundGoDuringWait(t *testing.T) {
	background := NewBackground(slog.New(slog.DiscardHandler))
	var wg sync.WaitGroup
	var finished atomic.Int64

	for range 50 {
		wg.Go(func() {
			background.Go(t.Context(), "test", func(context.Context) error {
				finished.Add(1)
				return nil
			})
		})
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
			defer cancel()
			_ = background.Wait(ctx)
		})
	}
	wg.Wait()
	err := background.Wait(t.Context())

	require.NoError(t, err)
	assert.Equal(t, int64(50), finished.Load())
}

// Wait gives up when its context ends before the work does.
func TestBackgroundWaitEnds(t *testing.T) {
	background := NewBackground(slog.New(slog.DiscardHandler))
	release := make(chan struct{})
	background.Go(t.Context(), "test", func(context.Context) error {
		<-release
		return nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err := background.Wait(ctx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	require.NoError(t, background.Wait(t.Context()))
}
