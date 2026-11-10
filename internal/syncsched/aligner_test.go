package syncsched_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/syncsched"
)

func TestNewRefusesAMissingDependency(t *testing.T) {
	r := newRig(t)
	full := syncsched.Deps{
		Writer: r.writer, Prober: embedded.New(r.store), Status: r.status,
		Policy: func() language.Policy { return enPolicy(t) },
	}
	for name, drop := range map[string]func(*syncsched.Deps){
		"Writer": func(d *syncsched.Deps) { d.Writer = nil },
		"Prober": func(d *syncsched.Deps) { d.Prober = nil },
		"Status": func(d *syncsched.Deps) { d.Status = nil },
		"Policy": func(d *syncsched.Deps) { d.Policy = nil },
	} {
		t.Run(name, func(t *testing.T) {
			d := full
			drop(&d)
			defer func() {
				if recover() == nil {
					t.Errorf("New without %s returned", name)
				}
			}()
			syncsched.New(d)
		})
	}
	if syncsched.New(full) == nil {
		t.Error("New with no Logger returned nil")
	}
}
