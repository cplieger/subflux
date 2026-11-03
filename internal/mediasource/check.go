package mediasource

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
)

// pingBudget bounds one entry's connection check, whoever asks for it.
const pingBudget = 10 * time.Second

// CheckSaved is the sources half of the Save check: it pings each entry of
// next that prev lacks, whose Type differs from prev's entry with its ID,
// or whose value differs in a field its kind's schema marks Required or
// Secret, so the adapter's own schema decides which fields name the
// connection. The entries are pinged in parallel, so a Save waits at most
// one budget; each failure is a field error at sources[Index].
func CheckSaved(ctx context.Context, kinds []AdapterKind, deps Deps, prev, next []Config) []formschema.FieldError {
	var due []Config
	for _, c := range next {
		if changedConnection(kinds, prev, &c) {
			due = append(due, c)
		}
	}
	errs := make([]error, len(due))
	var wg sync.WaitGroup
	for i, c := range due {
		wg.Go(func() { errs[i] = PingEntry(ctx, kinds, deps, c) })
	}
	wg.Wait()
	var out []formschema.FieldError
	for i, err := range errs {
		if err != nil {
			out = append(out, formschema.FieldError{Path: fmt.Sprintf("sources[%d]", due[i].Index), Message: pingMessage(err)})
		}
	}
	return out
}

func changedConnection(kinds []AdapterKind, prev []Config, c *Config) bool {
	i := slices.IndexFunc(prev, func(p Config) bool { return p.ID == c.ID })
	if i < 0 || prev[i].Type != c.Type {
		return true
	}
	k := slices.IndexFunc(kinds, func(k AdapterKind) bool { return k.Type == c.Type })
	if k < 0 {
		return true
	}
	return valuesDiffer(kinds[k].Schema, prev[i].Values, c.Values, func(f formschema.Field) bool {
		return f.Required || f.Kind == formschema.Secret
	})
}

func pingMessage(err error) string {
	var se *SourceError
	if !errors.As(err, &se) {
		return line("the connection check failed: "+err.Error(), maxTitleBytes)
	}
	msg := "the connection check failed (" + se.Cause.String() + ")"
	if se.Err != nil {
		msg += ": " + se.Err.Error()
	}
	return line(msg, maxTitleBytes)
}

// PingEntry builds cfg's adapter with its kind's New and calls Ping under
// pingBudget, its answer mapped as a Set call's is; it builds no Set, so the
// save check and the connection test need nothing else a Set holds. An
// unknown type or a failing New is a plain error.
func PingEntry(ctx context.Context, kinds []AdapterKind, deps Deps, cfg Config) error { //nolint:gocritic // hugeParam: Config is the section value AdapterKind.New takes
	k := slices.IndexFunc(kinds, func(k AdapterKind) bool { return k.Type == cfg.Type })
	if k < 0 {
		return fmt.Errorf("mediasource: source %s: unknown type %q", cfg.ID, cfg.Type)
	}
	ad, err := kinds[k].New(cfg, deps)
	if err != nil {
		return fmt.Errorf("mediasource: source %s: %w", cfg.ID, err)
	}
	ctx, cancel := context.WithTimeout(ctx, pingBudget)
	defer cancel()
	a := &answer{src: &source{id: cfg.ID}, op: "Ping"}
	return fail(ctx, a, ad.Ping(ctx))
}
