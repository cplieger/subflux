package settings

import (
	"context"
	"log/slog"
	"slices"
)

// processLogger logs each record through slog.Default() as it is when the
// record is logged: a host policy keeps the logger it is parsed with, and
// Load runs before the process installs its handler.
var processLogger = slog.New(processHandler{})

// processHandler is slog.Default()'s handler at each call, derived with the
// attrs and groups this handler was derived with.
type processHandler struct {
	derive []func(slog.Handler) slog.Handler
}

func (h processHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.current().Enabled(ctx, level)
}

//nolint:gocritic // hugeParam: slog.Handler fixes Handle's signature.
func (h processHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.current().Handle(ctx, r)
}

func (h processHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.with(func(d slog.Handler) slog.Handler { return d.WithAttrs(attrs) })
}

func (h processHandler) WithGroup(name string) slog.Handler {
	return h.with(func(d slog.Handler) slog.Handler { return d.WithGroup(name) })
}

func (h processHandler) with(step func(slog.Handler) slog.Handler) processHandler {
	return processHandler{derive: append(slices.Clip(h.derive), step)}
}

func (h processHandler) current() slog.Handler {
	d := slog.Default().Handler()
	for _, step := range h.derive {
		d = step(d)
	}
	return d
}
