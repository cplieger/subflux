package settings

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/formschema"
)

func onlyError(t *testing.T, errs []formschema.FieldError, path string) {
	t.Helper()
	if len(errs) != 1 || errs[0].Path != path {
		t.Errorf("errors %v, want one at %s", errs, path)
	}
}

func TestSaveRefusesDisableAuthChange(t *testing.T) {
	t.Run("a first Save from Unconfigured", func(t *testing.T) {
		path := fixture(t, "")
		s := serve(path, nil, nil)
		_, errs, err := save(t, s, "auth: {disable_auth: true}\n"+validLanguages)
		if err != nil {
			t.Fatal(err)
		}
		onlyError(t, errs, "auth.disable_auth")
		if s.Snapshot().Mode() != Unconfigured {
			t.Errorf("mode %v, want Unconfigured after the refusal", s.Snapshot().Mode())
		}
		if _, errs, err := save(t, s, "auth: {disable_auth: false}\n"+validLanguages); err != nil || errs != nil {
			t.Errorf("Save keeping it false = %v, %v; want it committed", errs, err)
		}
	})
	t.Run("a stored unparsable value counts as false", func(t *testing.T) {
		s := serve(fixture(t, "auth: {disable_auth: maybe}\n"+validLanguages), nil, nil)
		if s.Snapshot().Mode() != Failed {
			t.Fatalf("mode %v, want Failed", s.Snapshot().Mode())
		}
		_, errs, err := save(t, s, "auth: {disable_auth: true}\n"+validLanguages)
		if err != nil {
			t.Fatal(err)
		}
		onlyError(t, errs, "auth.disable_auth")
		if _, errs, err := save(t, s, "auth: {disable_auth: false}\n"+validLanguages); err != nil || errs != nil {
			t.Errorf("Save of false = %v, %v; want it committed", errs, err)
		}
	})
	t.Run("the stored value is read alone, beside a broken key", func(t *testing.T) {
		s := serve(fixture(t, "auth: {disable_auth: true, bogus: 1}\n"+validLanguages), nil, nil)
		_, errs, err := save(t, s, "auth: {disable_auth: false}\n"+validLanguages)
		if err != nil {
			t.Fatal(err)
		}
		onlyError(t, errs, "auth.disable_auth")
		if _, errs, err := save(t, s, "auth: {disable_auth: true}\n"+validLanguages); err != nil || errs != nil {
			t.Errorf("Save repairing the section and keeping true = %v, %v; want it committed", errs, err)
		}
	})
	t.Run("a stored value kept in another spelling", func(t *testing.T) {
		s := serve(fixture(t, "auth: {disable_auth: True}\n"+validLanguages), nil, nil)
		if _, errs, err := save(t, s, "auth: {disable_auth: true}\n"+validLanguages); err != nil || errs != nil {
			t.Errorf("Save of true over a stored True = %v, %v; want it committed", errs, err)
		}
	})
	t.Run("a stored reference is read expanded", func(t *testing.T) {
		t.Setenv("SUBFLUX_BYPASS", "true")
		s := serve(fixture(t, "auth: {disable_auth: '${SUBFLUX_BYPASS}'}\n"+validLanguages), nil, nil)
		if auth, ok := s.Snapshot().Auth(); !ok || !accounts.BypassEnabled(auth) {
			t.Fatalf("Auth() bypass %v, %v; want the expanded true", accounts.BypassEnabled(auth), ok)
		}
		if _, errs, err := s.Save(t.Context(), s.Snapshot().Document(), s.Snapshot().Revision()); err != nil || errs != nil {
			t.Errorf("Save(Document()) = %v, %v; want it committed", errs, err)
		}
		_, errs, err := save(t, s, "auth: {disable_auth: false}\n"+validLanguages)
		if err != nil {
			t.Fatal(err)
		}
		onlyError(t, errs, "auth.disable_auth")
	})
}

func TestCheckOrigins(t *testing.T) {
	const listed = "server: {allowed_hosts: [nas.example]}\nauth: {passkey_origins: ['https://nas.example']}\n" + validLanguages
	const dropped = "server: {allowed_hosts: [other.example]}\nauth: {passkey_origins: ['https://nas.example']}\n" + validLanguages
	t.Run("a Save changing only allowed_hosts is checked", func(t *testing.T) {
		path := fixture(t, listed)
		s := serve(path, nil, nil)
		if m := s.Snapshot().Mode(); m != ModeActive {
			t.Fatalf("mode %v, want Active", m)
		}
		_, errs, err := save(t, s, dropped)
		if err != nil {
			t.Fatal(err)
		}
		onlyError(t, errs, "auth.passkey_origins[0]")
		if fileText(t, path) != listed {
			t.Error("a refused Save wrote the file")
		}
	})
	t.Run("Load of a file whose origin is not listed is Failed", func(t *testing.T) {
		snap := loadText(t, dropped)
		errs, ok := snap.Failure()
		if !ok {
			t.Fatalf("mode %v, want Failed", snap.Mode())
		}
		onlyError(t, errs, "auth.passkey_origins[0]")
		if auth, ok := snap.Auth(); ok || accounts.MethodRecord(auth) != accounts.MethodRecord(accounts.Config{}) {
			t.Errorf("Auth() = %+v, %v; want the defaults, false, for a section its cross check refused", accounts.MethodRecord(auth), ok)
		}
		if _, ok := snap.Server(); !ok {
			t.Error("Server() lost a section that parsed")
		}
	})
	t.Run("the check runs beside a failed section", func(t *testing.T) {
		snap := loadText(t, "server: {allowed_hosts: [other.example]}\nauth: {passkey_origins: ['https://nas.example']}\n")
		errs, _ := snap.Failure()
		if !slices.Contains(errPaths(errs), "auth.passkey_origins[0]") {
			t.Errorf("errors %v, want the origin refused beside the absent languages section", errs)
		}
		if _, ok := snap.Auth(); ok {
			t.Error("Auth() kept a section its cross check refused")
		}
	})
	t.Run("a failed server section leaves the auth section unchecked", func(t *testing.T) {
		snap := loadText(t, "server: {allowed_hosts: ['http://nas.example']}\nauth: {passkey_origins: ['https://nas.example']}\n"+validLanguages)
		errs, _ := snap.Failure()
		if !slices.Equal(errPaths(errs), []string{"server.allowed_hosts[0]"}) {
			t.Errorf("errors %v, want only the server entry", errs)
		}
		if _, ok := snap.Auth(); !ok {
			t.Error("Auth() dropped a section that parsed")
		}
	})
}

func TestFailedKeepsParsedAuth(t *testing.T) {
	t.Run("a valid auth section beside a broken one", func(t *testing.T) {
		snap := loadText(t, "auth: {password_login: false}\nbogus: 1\n"+validLanguages)
		if snap.Mode() != Failed {
			t.Fatalf("mode %v, want Failed", snap.Mode())
		}
		auth, ok := snap.Auth()
		if !ok || accounts.MethodRecord(auth).PasswordLogin {
			t.Errorf("Auth() = password %v, %v; want the parsed section, password sign-in off", accounts.MethodRecord(auth).PasswordLogin, ok)
		}
	})
	t.Run("a broken auth section", func(t *testing.T) {
		snap := loadText(t, "auth: {password_login: false, session_idle_timeout: 1s}\n"+validLanguages)
		auth, ok := snap.Auth()
		if ok || accounts.MethodRecord(auth) != accounts.MethodRecord(accounts.Config{}) {
			t.Errorf("Auth() = %+v, %v; want the defaults, false", accounts.MethodRecord(auth), ok)
		}
	})
	t.Run("a tag-refused auth section", func(t *testing.T) {
		snap := loadText(t, "auth: {password_login: !!bool false}\n"+validLanguages)
		auth, ok := snap.Auth()
		if ok || !accounts.MethodRecord(auth).PasswordLogin {
			t.Errorf("Auth() = password %v, %v; want the defaults, false", accounts.MethodRecord(auth).PasswordLogin, ok)
		}
	})
	t.Run("a root failure", func(t *testing.T) {
		auth, ok := loadText(t, "- auth\n").Auth()
		if ok || accounts.MethodRecord(auth) != accounts.MethodRecord(accounts.Config{}) {
			t.Errorf("Auth() = %+v, %v; want the defaults, false", accounts.MethodRecord(auth), ok)
		}
	})
	t.Run("Unconfigured runs the defaults", func(t *testing.T) {
		auth, ok := Load(fixture(t, ""), Registries{}).Auth()
		if !ok || accounts.MethodRecord(auth) != accounts.MethodRecord(accounts.Config{}) {
			t.Errorf("Auth() = %+v, %v; want the defaults, true", accounts.MethodRecord(auth), ok)
		}
	})
	t.Run("the zero Snapshot", func(t *testing.T) {
		if _, ok := (Snapshot{}).Auth(); ok {
			t.Error("the zero Snapshot's Auth() is true")
		}
	})
}

// TestFileAuthIsTheSectionAsParsed: FileAuth keeps a section a cross check
// or Boot refused, and reads a section that failed to parse, or no file, as
// the defaults.
func TestFileAuthIsTheSectionAsParsed(t *testing.T) {
	const bypass = "auth: {disable_auth: true}\n" + validLanguages
	t.Run("an Active section", func(t *testing.T) {
		if !accounts.BypassEnabled(loadText(t, bypass).FileAuth()) {
			t.Error("FileAuth() lost disable_auth: true")
		}
	})
	t.Run("a section its cross check refused", func(t *testing.T) {
		snap := loadText(t, "server: {allowed_hosts: [other.example]}\nauth: {passkey_origins: ['https://nas.example'], disable_auth: true}\n"+validLanguages)
		if _, ok := snap.Auth(); ok || !accounts.BypassEnabled(snap.FileAuth()) {
			t.Error("FileAuth() lost the values of a section its cross check refused")
		}
	})
	t.Run("a section Boot refused", func(t *testing.T) {
		b := boot(t, bypass, refusing, acceptAuth)
		if err := b.svc.Boot(t.Context()); err != nil {
			t.Fatalf("Boot = %v", err)
		}
		snap := b.svc.Snapshot()
		if _, ok := snap.Auth(); ok || !accounts.BypassEnabled(snap.FileAuth()) {
			t.Error("FileAuth() lost the values of a section Boot refused")
		}
	})
	t.Run("a section that failed to parse", func(t *testing.T) {
		if accounts.BypassEnabled(loadText(t, "auth: {password_login: nope, disable_auth: true}\n"+validLanguages).FileAuth()) {
			t.Error("FileAuth() of a section that failed to parse is not the defaults")
		}
	})
	t.Run("no file", func(t *testing.T) {
		if accounts.MethodRecord(Load(fixture(t, ""), Registries{}).FileAuth()) != accounts.MethodRecord(accounts.Config{}) {
			t.Error("FileAuth() with no file is not the defaults")
		}
	})
	t.Run("the zero Snapshot", func(t *testing.T) {
		if accounts.MethodRecord((Snapshot{}).FileAuth()) != accounts.MethodRecord(accounts.Config{}) {
			t.Error("the zero Snapshot's FileAuth() is not the defaults")
		}
	})
}

func TestLoggedValueThroughExpansion(t *testing.T) {
	t.Setenv("SUBFLUX_CLIENT", "a\xe9")
	snap := loadText(t, "auth: {oidc_client_id: '${SUBFLUX_CLIENT}'}\n"+validLanguages)
	errs, ok := snap.Failure()
	if !ok {
		t.Fatalf("mode %v, want Failed", snap.Mode())
	}
	onlyError(t, errs, "auth.oidc_client_id")
	if _, ok := snap.Auth(); ok {
		t.Error("Auth() kept a section holding invalid UTF-8")
	}
}
