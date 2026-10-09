package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// checkerProvider is a fakeProvider that also offers a credential check,
// answering with whatever the test handed it.
type checkerProvider struct {
	fakeProvider
	err error
}

func (p *checkerProvider) CheckCredentials(context.Context) error { return p.err }

// newChecker fills the embedded name by assignment rather than in a literal:
// the embedded field's own type name is redundant there, and eliding it is a
// compile error for an embedded struct.
func newChecker(name string, err error) *checkerProvider {
	p := &checkerProvider{err: err}
	p.name = name
	return p
}

// TestCredentialCheck pins the registered capability, which is one fact with two
// readers: the schema renders a test control from it and the endpoint refuses a
// request naming a provider it answers false for.
func TestCredentialCheck(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterCredentialCheck("offers", true)
	r.RegisterCredentialCheck("declines", false)

	tests := []struct {
		name subflux.ProviderID
		want bool
	}{
		{name: "offers", want: true},
		{name: "declines", want: false},
		{name: "never-registered", want: false},
	}
	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			t.Parallel()
			if got := r.CredentialCheck(tt.name); got != tt.want {
				t.Errorf("CredentialCheck(%q) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}

// TestRegistry_CheckCredentials pins what the endpoint's provider arm reads: the
// verdict a provider returned, and a refusal for the two ways a check can be
// unavailable. A factory failure reports as a refusal because every factory here
// fails only on a credential the operator left out, and sending them to look at
// the network for a blank field would be the wrong remedy.
func TestRegistry_CheckCredentials(t *testing.T) {
	t.Parallel()
	unreachable := errors.New("dial tcp: connection refused")
	refused := &subflux.AuthError{Msg: "refused"}

	tests := []struct {
		factoryErr  error
		checkErr    error
		wantErr     error
		name        string
		probe       subflux.ProviderID
		noChecker   bool
		wantRefused bool
	}{
		{name: "accepted credentials", probe: "p"},
		{
			name: "refused credentials", probe: "p", checkErr: refused,
			wantErr: refused, wantRefused: true,
		},
		{
			name: "unreachable service", probe: "p", checkErr: unreachable,
			wantErr: unreachable,
		},
		{
			name: "a factory failure is a refusal", probe: "p", factoryErr: errors.New("api_key is required"),
			wantRefused: true,
		},
		{
			name: "a provider without a checker", probe: "p", noChecker: true,
			wantErr: errNoCredentialCheck,
		},
		{
			name: "an unregistered name", probe: "absent",
			wantErr: errNoCredentialCheck,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := NewRegistry()
			r.Register("p", func(context.Context, map[string]any) (Provider, error) {
				if tt.factoryErr != nil {
					return nil, tt.factoryErr
				}
				if tt.noChecker {
					return &fakeProvider{name: "p"}, nil
				}
				return newChecker("p", tt.checkErr), nil
			})

			err := r.CheckCredentials(t.Context(), tt.probe, nil)

			wantErr := tt.wantErr != nil || tt.wantRefused
			if (err != nil) != wantErr {
				t.Fatalf("CheckCredentials(%q) = %v, want an error: %t", tt.probe, err, wantErr)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("CheckCredentials(%q) = %v, want one matching %v", tt.probe, err, tt.wantErr)
			}
			_, gotRefused := errors.AsType[*subflux.AuthError](err)
			if gotRefused != tt.wantRefused {
				t.Errorf("CheckCredentials(%q) error %v is *subflux.AuthError = %t, want %t",
					tt.probe, err, gotRefused, tt.wantRefused)
			}
		})
	}
}

// The check must see the same settings a save would activate, schema defaults
// filled in: a provider whose declared default decides which endpoint or scheme
// it talks to would otherwise be probed in a shape the running engine never uses.
func TestRegistry_CheckCredentials_normalizes_settings(t *testing.T) {
	t.Parallel()
	var got map[string]any
	r := NewRegistry()
	r.RegisterSchema("p", "P", []subflux.ProviderSchemaField{
		{Key: "api_key", Type: "secret"},
		{Key: "use_hash", Type: "bool", Default: "true"},
	})
	r.Register("p", func(_ context.Context, settings map[string]any) (Provider, error) {
		got = settings
		return newChecker("p", nil), nil
	})

	if err := r.CheckCredentials(t.Context(), "p", map[string]any{"api_key": "k"}); err != nil {
		t.Fatalf("CheckCredentials() = %v, want nil", err)
	}
	if got["api_key"] != "k" {
		t.Errorf("CheckCredentials() passed api_key %v, want %q", got["api_key"], "k")
	}
	if got["use_hash"] != true {
		t.Errorf("CheckCredentials() passed use_hash %v, want the schema default true", got["use_hash"])
	}
}
