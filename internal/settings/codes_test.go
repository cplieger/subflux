package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/go/httpwire/httpwiretest"
)

func TestCodesCoverFailureTable(t *testing.T) {
	const text = "server: {log_level: debug}\n" + validLanguages
	arms := []struct {
		call func(*testing.T) error
		name string
		want string
	}{
		{func(t *testing.T) error {
			s := serve(fixture(t, ""), func(context.Context, Active) error { return errActivate }, nil)
			_, _, err := save(t, s, text)
			return err
		}, "Save whose activate fails", "activation_failed"},
		{func(t *testing.T) error {
			s := serve(fixture(t, ""), nil, nil)
			if err := s.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			_, _, err := save(t, s, text)
			return err
		}, "Save after Shutdown", "settings_closing"},
		{func(t *testing.T) error {
			s := serve(fixture(t, ""), nil, nil)
			_, _, err := s.Save(t.Context(), node(t, text), "a revision never read")
			return err
		}, "Save from another revision", "stale_version"},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			if got := codeOf(arm.call(t), Owner); got != arm.want {
				t.Errorf("code %q, want %q", got, arm.want)
			}
		})
	}
}

func TestOwnerSentinels(t *testing.T) {
	httpwiretest.Sentinels(t, Owner)
}

// codeOf is the code of the first entry of owners' Codes that err matches by
// errors.Is, "" when none does: the lookup httpwire's answer mapping makes.
func codeOf(err error, owners ...*httpwire.Owner) string {
	for _, o := range owners {
		for _, c := range o.Codes {
			if errors.Is(err, c.Err) {
				return c.Code
			}
		}
	}
	return ""
}
