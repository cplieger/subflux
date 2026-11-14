package settings

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/formschema"
)

var guardRefusal = []formschema.FieldError{{Path: "auth.password_login", Message: "would leave 1 user with no way to sign in"}}

func refusing(context.Context, Active) error {
	return &formschema.RefusedError{Fields: guardRefusal}
}

func TestSaveRefusedByActivation(t *testing.T) {
	const before = "server: {log_level: info}\n" + validLanguages
	path := fixture(t, before)
	rec := capture.Default(t)
	s := serve(path, refusing, nil)
	old := s.Snapshot()
	snap, errs, err := save(t, s, "server: {log_level: debug}\nauth: {password_login: false}\n"+validLanguages)
	if err != nil || !slices.Equal(errs, guardRefusal) {
		t.Fatalf("Save = %v, %v; want the refusal's field error and no error", errs, err)
	}
	if snap.p != old.p || s.Snapshot().p != old.p {
		t.Error("a refused Save replaced the snapshot")
	}
	if got := fileText(t, path); got != before {
		t.Errorf("config.yaml = %q, want the baseline restored", got)
	}
	if signalled(s) {
		t.Error("a refused Save signalled Changed")
	}
	if n := rec.CountLevel(slog.LevelWarn, "refused"); n != 1 || !rec.HasAttr("refused", "path", path) {
		t.Errorf("%d WARN lines naming the refusal, want one naming %s", n, path)
	}
	t.Run("another activation error stays ErrActivation", func(t *testing.T) {
		s := serve(fixture(t, before), func(context.Context, Active) error { return errActivate }, nil)
		if _, errs, err := save(t, s, "server: {log_level: debug}\n"+validLanguages); !errors.Is(err, ErrActivation) || errs != nil {
			t.Errorf("Save = %v, %v; want ErrActivation", errs, err)
		}
	})
}

// booted is a Service over text's load with fakes counting each call.
type booted struct {
	svc               *Service
	activated, authed int
	authSeen          accounts.Config
}

func boot(t *testing.T, text string, activate func(context.Context, Active) error, activateAuth func(context.Context, accounts.Config) error) *booted {
	t.Helper()
	b := &booted{}
	path := fixture(t, text)
	b.svc = NewService(path, Registries{}, Load(path, Registries{}),
		func(ctx context.Context, a Active) error {
			b.activated++
			return activate(ctx, a)
		},
		func(ctx context.Context, c accounts.Config) error {
			b.authed++
			b.authSeen = c
			return activateAuth(ctx, c)
		}, nil)
	return b
}

func accept(context.Context, Active) error              { return nil }
func acceptAuth(context.Context, accounts.Config) error { return nil }
func refuseAuth(context.Context, accounts.Config) error {
	return &formschema.RefusedError{Fields: guardRefusal}
}
func failAuth(context.Context, accounts.Config) error { return errActivate }

func TestBootRefusedByActivation(t *testing.T) {
	const active = "server: {log_level: debug}\n" + validLanguages
	t.Run("an Active load refused turns Failed", func(t *testing.T) {
		b := boot(t, active, refusing, acceptAuth)
		if err := b.svc.Boot(t.Context()); err != nil {
			t.Fatalf("Boot = %v", err)
		}
		snap := b.svc.Snapshot()
		if errs, failed := snap.Failure(); !failed || !slices.Equal(errs, guardRefusal) {
			t.Errorf("Failure() = %v, %v; want Failed with the refusal", errs, failed)
		}
		if srv, ok := snap.Server(); !ok || srv.LogLevel() != slog.LevelDebug {
			t.Error("the refusal dropped the server section")
		}
		if _, ok := snap.Auth(); ok {
			t.Error("Auth() is true after its refusal")
		}
		if b.activated != 1 || b.authed != 0 {
			t.Errorf("activate ran %d times, activateAuth %d; want 1 and 0", b.activated, b.authed)
		}
	})
	t.Run("another error from an Active load is returned", func(t *testing.T) {
		b := boot(t, active, func(context.Context, Active) error { return errActivate }, acceptAuth)
		if err := b.svc.Boot(t.Context()); !errors.Is(err, errActivate) {
			t.Errorf("Boot = %v, want the activation's error", err)
		}
		if b.svc.Snapshot().Mode() != ModeActive {
			t.Error("a failed activation changed the mode")
		}
	})
	t.Run("a Failed load with a parsed auth section runs the auth half", func(t *testing.T) {
		b := boot(t, "auth: {password_login: false}\nbogus: 1\n"+validLanguages, accept, refuseAuth)
		loadErrs, _ := b.svc.Snapshot().Failure()
		if err := b.svc.Boot(t.Context()); err != nil {
			t.Fatalf("Boot = %v", err)
		}
		if b.activated != 0 || b.authed != 1 || accounts.MethodRecord(b.authSeen).PasswordLogin {
			t.Errorf("activate %d, activateAuth %d with password %v; want 0, 1 and the parsed section",
				b.activated, b.authed, accounts.MethodRecord(b.authSeen).PasswordLogin)
		}
		errs, failed := b.svc.Snapshot().Failure()
		if !failed || !slices.Equal(errs, append(loadErrs, guardRefusal...)) {
			t.Errorf("Failure() = %v, want the load's errors then the refusal", errs)
		}
		if _, ok := b.svc.Snapshot().Auth(); ok {
			t.Error("Auth() is true after its refusal")
		}
	})
	t.Run("another error from the auth half is returned", func(t *testing.T) {
		b := boot(t, "auth: {password_login: false}\nbogus: 1\n"+validLanguages, accept, failAuth)
		old := b.svc.Snapshot()
		if err := b.svc.Boot(t.Context()); !errors.Is(err, errActivate) {
			t.Errorf("Boot = %v, want the auth half's error", err)
		}
		if b.svc.Snapshot().p != old.p {
			t.Error("a failed auth half changed the snapshot")
		}
	})
	t.Run("Unconfigured runs the auth half on the defaults", func(t *testing.T) {
		b := boot(t, "", accept, refuseAuth)
		if err := b.svc.Boot(t.Context()); err != nil {
			t.Fatalf("Boot = %v", err)
		}
		if b.authed != 1 || accounts.MethodRecord(b.authSeen) != accounts.MethodRecord(accounts.Config{}) {
			t.Errorf("activateAuth ran %d times on %+v, want once on the defaults", b.authed, accounts.MethodRecord(b.authSeen))
		}
		snap := b.svc.Snapshot()
		if errs, failed := snap.Failure(); !failed || !slices.Equal(errs, guardRefusal) {
			t.Errorf("Failure() = %v, %v; want Failed with the refusal alone", errs, failed)
		}
		if _, ok := snap.Server(); !ok {
			t.Error("Server() is false")
		}
		if _, ok := snap.Auth(); ok {
			t.Error("Auth() is true after its refusal")
		}
	})
	t.Run("Unconfigured accepted stays Unconfigured", func(t *testing.T) {
		b := boot(t, "", accept, acceptAuth)
		if err := b.svc.Boot(t.Context()); err != nil || b.svc.Snapshot().Mode() != Unconfigured || b.authed != 1 {
			t.Errorf("Boot = %v, mode %v, activateAuth %d; want nil, Unconfigured, 1", err, b.svc.Snapshot().Mode(), b.authed)
		}
	})
	t.Run("a Failed load whose auth section failed runs neither", func(t *testing.T) {
		b := boot(t, "auth: {password_login: nope}\n"+validLanguages, accept, refuseAuth)
		if err := b.svc.Boot(t.Context()); err != nil {
			t.Fatalf("Boot = %v", err)
		}
		if b.activated != 0 || b.authed != 0 {
			t.Errorf("activate %d, activateAuth %d; want neither", b.activated, b.authed)
		}
	})
	t.Run("nil activations run nothing", func(t *testing.T) {
		for _, text := range []string{active, ""} {
			path := fixture(t, text)
			s := NewService(path, Registries{}, Load(path, Registries{}), nil, nil, nil)
			if err := s.Boot(t.Context()); err != nil {
				t.Errorf("Boot(%q) = %v", text, err)
			}
		}
	})
}

// TestBootedIsWhatBootLeftInForce: Booted is the zero Snapshot until a Boot
// returns nil, then the snapshot that Boot left, whatever a later Save
// commits.
func TestBootedIsWhatBootLeftInForce(t *testing.T) {
	const active = "auth: {disable_auth: true}\n" + validLanguages
	t.Run("an accepted Boot", func(t *testing.T) {
		b := boot(t, active, accept, acceptAuth)
		if _, ok := b.svc.Booted().Auth(); ok {
			t.Error("Booted().Auth() is true before Boot")
		}
		if err := b.svc.Boot(t.Context()); err != nil {
			t.Fatalf("Boot = %v", err)
		}
		booted := b.svc.Booted()
		if booted.p == nil || booted.p != b.svc.Snapshot().p {
			t.Fatal("Booted() is not the snapshot Boot left in force")
		}
		if _, errs, err := save(t, b.svc, "server: {log_level: debug}\n"+active); err != nil || errs != nil {
			t.Fatalf("Save = %v, %v", errs, err)
		}
		if b.svc.Snapshot().p == booted.p || b.svc.Booted().p != booted.p {
			t.Error("Booted() moved with the Save")
		}
	})
	t.Run("a refused Boot", func(t *testing.T) {
		b := boot(t, active, refusing, acceptAuth)
		if err := b.svc.Boot(t.Context()); err != nil {
			t.Fatalf("Boot = %v", err)
		}
		booted := b.svc.Booted()
		if _, ok := booted.Auth(); ok || booted.Mode() != Failed || booted.p != b.svc.Snapshot().p {
			t.Errorf("Booted() = mode %v, want the refused Failed snapshot in force", booted.Mode())
		}
	})
	t.Run("a failed Boot", func(t *testing.T) {
		b := boot(t, active, func(context.Context, Active) error { return errActivate }, acceptAuth)
		if err := b.svc.Boot(t.Context()); !errors.Is(err, errActivate) {
			t.Fatalf("Boot = %v, want the activation's error", err)
		}
		if b.svc.Booted().p != nil {
			t.Error("Booted() answers a snapshot after a failed Boot")
		}
	})
}
