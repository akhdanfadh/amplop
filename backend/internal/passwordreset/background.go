package passwordreset

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"inbok/internal/systememail"
)

// backgroundTimeout bounds one piece of background work: a few database
// statements and one SMTP exchange, which smtp bounds at 8 seconds.
const backgroundTimeout = 30 * time.Second

// Background runs password reset work that the request starting it does not
// wait for, and lets serve's shutdown wait for it.
//
// DESIGN: the work runs in the serve process, so a crash or a shutdown that
// outlasts its wait loses it. ADR030 records why this comes before durable
// background jobs, and DEBT.md when to move.
type Background struct {
	logger  *slog.Logger
	timeout time.Duration

	mu      sync.Mutex
	running int           // work started and not finished
	idle    chan struct{} // closed when running drops to zero
}

// NewBackground returns a Background that logs failed work with logger.
func NewBackground(logger *slog.Logger) *Background {
	idle := make(chan struct{})
	close(idle)
	return &Background{logger: logger, timeout: backgroundTimeout, idle: idle}
}

// Go starts work in a new goroutine and returns without waiting for it.
// work gets a context with ctx's values, such as its request ID, but not its
// cancellation, and with its own deadline. Go logs the error work returns,
// or a panic, by purpose.
func (b *Background) Go(ctx context.Context, purpose string, work func(context.Context) error) {
	b.mu.Lock()
	if b.running == 0 {
		b.idle = make(chan struct{})
	}
	b.running++
	b.mu.Unlock()

	// The request's context ends once its response is sent.
	ctx = context.WithoutCancel(ctx)
	go func() {
		defer b.finish()
		ctx, cancel := context.WithTimeout(ctx, b.timeout)
		defer cancel()
		defer func() {
			// A panic here would end the whole server, not one request. Its
			// value is not logged: it could hold what a returned error would
			// have kept out of the log, such as a mail server's reply.
			if recover() != nil {
				b.logger.ErrorContext(ctx, "password reset work panicked", "purpose", purpose, "stack", string(debug.Stack()))
			}
		}()
		if err := work(ctx); err != nil {
			b.logFailure(ctx, purpose, err)
		}
	}()
}

// finish records that one piece of work ended.
func (b *Background) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running--
	if b.running == 0 {
		close(b.idle)
	}
}

// logFailure logs err from the work named purpose.
func (b *Background) logFailure(ctx context.Context, purpose string, err error) {
	// The mailer already logged a failed send, with what is safe to keep of
	// the server's reply; the reply can quote a message that holds a secret.
	if failed, ok := errors.AsType[*systememail.SendError](err); ok {
		b.logger.WarnContext(ctx, "password reset email not sent", "purpose", purpose, "reason", failed.Reason)
		return
	}
	b.logger.ErrorContext(ctx, "password reset work failed", "purpose", purpose, "err", err)
}

// Wait returns nil once no work started by [Background.Go] is running, or
// ctx's error if ctx ends first. Work started while it waits counts too.
func (b *Background) Wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		idle := b.idle
		b.mu.Unlock()
		select {
		case <-idle:
			// Work started after idle closed made a new channel.
			b.mu.Lock()
			running := b.running
			b.mu.Unlock()
			if running == 0 {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
