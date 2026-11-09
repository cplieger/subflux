package settings

import (
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/sentineltest"
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
		}, "Save from another revision", "settings_conflict"},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			if got := sentineltest.CodeOf(Codes, arm.call(t)); got != arm.want {
				t.Errorf("code %q, want %q", got, arm.want)
			}
		})
	}

	sentineltest.Placed(t, Codes, map[string]error{
		"ErrActivation": ErrActivation,
		"ErrClosed":     ErrClosed,
		"ErrConflict":   ErrConflict,
	}, nil)
}
