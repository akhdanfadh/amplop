// Package logging builds inbok's loggers and adds request_id to lines logged
// with a request's context.
//
// DESIGN: cmd/inbok builds one root logger with New and passes it to every
// service and to httpapi, so a line logged with a request's context carries
// its request_id wherever it is written. Alternatives were exporting
// httpapi's wrapper, which would make features depend on the HTTP package
// for a logging rule, and wrapping loggers inside httpapi.New, which runs
// after cmd/inbok has built the services. The logger stays a dependency
// passed in, not a context value, so tests can hand a service its own; only
// the request ID travels in the context. The level stays slog's default,
// Info, until something logs at Debug.
package logging

import (
	"context"
	"io"
	"log/slog"
	"slices"
)

type requestIDKey struct{}

// New returns a logger that writes text lines to w at Info level and above,
// and adds request_id to every line logged with a context from
// [WithRequestID].
func New(w io.Writer) *slog.Logger {
	return slog.New(Handler(slog.NewTextHandler(w, nil)))
}

// Handler returns h wrapped to add request_id to every record logged with a
// context from [WithRequestID], e.g. to log JSON in tests.
func Handler(h slog.Handler) slog.Handler {
	return requestIDHandler{h}
}

// WithRequestID returns a copy of ctx carrying id, which loggers from [New]
// and [Handler] add to lines logged with it as request_id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// requestIDHandler adds request_id to records logged with a context from
// [WithRequestID].
type requestIDHandler struct {
	slog.Handler
}

// Handle adds ctx's request ID, if any, to record and passes it on.
func (h requestIDHandler) Handle(ctx context.Context, record slog.Record) error {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		record.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, record)
}

// WithAttrs returns the handler with attrs added, still adding request_id.
func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// The embedded method would return the bare inner handler and drop
	// request_id.
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

// WithGroup returns the handler with group name opened, still adding
// request_id.
func (h requestIDHandler) WithGroup(name string) slog.Handler {
	// The embedded method would return the bare inner handler and drop
	// request_id.
	return requestIDHandler{h.Handler.WithGroup(name)}
}

// WithoutAttrs returns a logger that writes what logger writes, minus every
// attribute whose key is one of keys, e.g. a connection URL a library logs.
func WithoutAttrs(logger *slog.Logger, keys ...string) *slog.Logger {
	return slog.New(withoutAttrsHandler{logger.Handler(), keys})
}

// withoutAttrsHandler leaves out attributes whose key is in keys, both on
// records and on attributes added with WithAttrs.
type withoutAttrsHandler struct {
	slog.Handler
	keys []string
}

// Handle passes record on without the attributes h leaves out.
func (h withoutAttrsHandler) Handle(ctx context.Context, record slog.Record) error {
	kept := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(a slog.Attr) bool {
		if !slices.Contains(h.keys, a.Key) {
			kept.AddAttrs(a)
		}
		return true
	})
	return h.Handler.Handle(ctx, kept)
}

// WithAttrs returns the handler with attrs added, minus those h leaves out.
func (h withoutAttrsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	kept := slices.DeleteFunc(slices.Clone(attrs), func(a slog.Attr) bool {
		return slices.Contains(h.keys, a.Key)
	})
	return withoutAttrsHandler{h.Handler.WithAttrs(kept), h.keys}
}

// WithGroup returns the handler with group name opened, still leaving out
// the same keys.
func (h withoutAttrsHandler) WithGroup(name string) slog.Handler {
	// The embedded method would return the bare inner handler and log the
	// keys again.
	return withoutAttrsHandler{h.Handler.WithGroup(name), h.keys}
}
