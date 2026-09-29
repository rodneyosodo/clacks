package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// Level parses a level name, falling back to info.
func Level(name string) slog.Level {
	lvl := slog.LevelInfo
	if strings.TrimSpace(name) == "" {
		return lvl
	}

	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(name)))); err == nil {
		return parsed
	}

	return lvl
}

// LevelFromEnv returns the level from CLACKS_LOG_LEVEL, if set.
func LevelFromEnv() string {
	return os.Getenv("CLACKS_LOG_LEVEL")
}

// InitCLI configures slog for a human at a terminal: one plain line per
// record, no timestamps, no level prefixes, no quoting of error text.
// Diagnostics go to stderr so stdout stays pipeable.
func InitCLI(level string) {
	slog.SetDefault(slog.New(newSimpleHandler(os.Stderr, Level(level))))
}

// InitServer configures slog for the server: JSON on stdout, one object per
// record, ready to be collected by a log pipeline.
func InitServer(level string) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: Level(level)})))
}

// simpleHandler renders records as bare lines: the message, then any
// attributes as key=value. It exists so the CLI reads like a program talking
// to a person rather than a log aggregator.
type simpleHandler struct {
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
	mu    *sync.Mutex
}

func newSimpleHandler(w io.Writer, level slog.Level) *simpleHandler {
	return &simpleHandler{w: w, level: level, mu: &sync.Mutex{}}
}

func (h *simpleHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

//nolint:gocritic // hugeParam: slog.Handler's interface mandates a value.
func (h *simpleHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		writeAttr(&b, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, a)

		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())

	return err
}

func (h *simpleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	next := &simpleHandler{w: h.w, level: h.level, mu: h.mu}
	next.attrs = make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	next.attrs = append(next.attrs, h.attrs...)
	next.attrs = append(next.attrs, attrs...)

	return next
}

// WithGroup returns h unchanged: attribute groups have no meaning in a flat
// line format, and nothing in clacks uses them.
func (h *simpleHandler) WithGroup(string) slog.Handler { return h }

func writeAttr(b *strings.Builder, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	// errors and strings carry spaces, quotes and colons; render them bare
	// rather than as a quoted blob, which is the whole point of this handler.
	var v string
	if err, ok := a.Value.Any().(error); ok {
		v = err.Error()
	} else {
		v = a.Value.String()
	}
	b.WriteByte(' ')
	b.WriteString(a.Key)
	b.WriteByte('=')
	b.WriteString(v)
}
