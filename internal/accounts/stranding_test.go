package accounts_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/account"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"go.yaml.in/yaml/v4"
)

const (
	password  = "correct horse battery staple 42"
	languages = "languages:\n  default: [{tag: en}]\n"
	issuerA   = "https://idp.example/a"
	issuerB   = "https://idp.example/b"
	rpA       = "nas.example"
	rpB       = "media.example"
)

// oidcOn is an enabled OIDC block for issuer, as auth keys.
func oidcOn(issuer string) string {
	return "  oidc_enabled: true\n  oidc_issuer: " + issuer + "\n  oidc_client_id: subflux\n  oidc_client_secret: s3cret\n  oidc_redirect_url: https://nas.example/api/auth/oidc/callback\n"
}

func passkeysAt(rp string) string { return "  passkey_origins: [https://" + rp + "]\n" }

type noDrops struct{}

func (noDrops) IncAuthEventsDropped() {}

// world is the settings service, the state store and the composed account
// service over one directory, wired as app wires them: activate writes the
// record judged by the method guard, and the pre-check runs in prepare.
type world struct {
	db          *statedb.Store
	s           *accounts.Service
	svc         *settings.Service
	acct        *account.Service
	path        string
	activations atomic.Int64
	// faultTx, when set, wraps the transaction the guard reads.
	faultTx func(account.Tx) account.Tx
	// beforeActivate and inUpdate, when set, run as activate starts and
	// inside its Update before the guard counts; afterActivate once its
	// Update committed, before Save publishes the snapshot.
	beforeActivate func()
	inUpdate       func()
	afterActivate  func()
	// precheckFault makes the pre-check's read fail.
	precheckFault bool
}

// newWorld boots over config text, no file when empty, on a fresh store.
func newWorld(t *testing.T, text string) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{path: filepath.Join(dir, "config.yaml")}
	if text != "" {
		if err := os.WriteFile(w.path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := statedb.Open(filepath.Join(dir, "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w.db = db
	guard := func() statedb.MethodGuard {
		g := w.s.MethodGuard()
		return func(ctx context.Context, tx account.Tx, before statedb.AuthMethodState, stored bool, next statedb.AuthMethodState) error {
			if w.inUpdate != nil {
				w.inUpdate()
			}
			if w.faultTx != nil {
				tx = w.faultTx(tx)
			}
			return g(ctx, tx, before, stored, next)
		}
	}
	activate := func(ctx context.Context, a settings.Active) error {
		w.activations.Add(1)
		if w.beforeActivate != nil {
			w.beforeActivate()
		}
		rec := accounts.MethodRecord(a.Auth)
		err := db.Activate(ctx, statedb.Activation{Auth: &rec, Guard: guard(), Languages: a.Languages, IgnoredCodecs: a.Embedded.Ignored()})
		if err == nil && w.afterActivate != nil {
			w.afterActivate()
		}
		return err
	}
	activateAuth := func(ctx context.Context, c accounts.Config) error {
		return db.ActivateAuth(ctx, accounts.MethodRecord(c), guard())
	}
	check := func(ctx context.Context, _, next settings.Active) []formschema.FieldError {
		if w.precheckFault {
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
		}
		return w.s.PreCheckMethods(ctx, next.Auth)
	}
	snap := settings.Load(w.path, settings.Registries{})
	w.svc = settings.NewService(w.path, settings.Registries{}, snap, activate, activateAuth, check)
	secrets, err := db.AuthSecrets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	w.s, err = accounts.New(t.Context(), accounts.Deps{
		Store: db.Accounts(), Secrets: secrets,
		Config:       func() (accounts.Config, bool) { return w.svc.Snapshot().Auth() },
		ServerParsed: func() bool { _, ok := w.svc.Snapshot().Server(); return ok },
		AllowedHosts: func() []string { return nil },
		Resolver:     func() clientaddr.Resolver { return clientaddr.New(nil) },
		Forwarder:    func(netip.Addr) bool { return false },
		Metrics:      noDrops{},
		Breach:       &http.Client{Timeout: time.Second},
		OIDC:         &http.Client{Timeout: time.Second},
		Logger:       slog.New(slog.DiscardHandler),
		Boot: func() accounts.Config {
			c, _ := w.svc.Booted().Auth()
			return c
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.stop() })
	w.acct = accounts.AccountOf(w.s)
	if err := w.svc.Boot(t.Context()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return w
}

// stop shuts the account service down.
func (w *world) stop() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = w.s.Run(ctx)
}

// user creates name holding a password.
func (w *world) user(t *testing.T, name string) *access.User {
	t.Helper()
	u, err := w.acct.CreateUser(t.Context(), account.OutOfBand("test"), account.NewUser{Username: name, Password: access.NewSecret(password), Role: access.RoleAdmin})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u
}

// edit rewrites u's stored row through f.
func (w *world) edit(t *testing.T, u *access.User, f func(*access.User)) {
	t.Helper()
	got, _, err := w.db.Accounts().UserByID(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	f(got)
	if err := w.db.Accounts().UpdateUser(t.Context(), got); err != nil {
		t.Fatal(err)
	}
}

func (w *world) dropPassword(t *testing.T, u *access.User) {
	t.Helper()
	w.edit(t, u, func(u *access.User) { u.PasswordHash = access.Hash{} })
}

func (w *world) link(t *testing.T, u *access.User, issuer string) {
	t.Helper()
	w.edit(t, u, func(x *access.User) { x.OIDC = access.OIDCIdentity{Issuer: issuer, Subject: u.Username} })
}

func (w *world) passkey(t *testing.T, u *access.User, rpID string) int64 {
	t.Helper()
	cred := &access.PasskeyCredential{
		UserID: u.ID, RPID: rpID, Name: rpID, CredentialID: fmt.Appendf(nil, "cred-%d-%s", u.ID, rpID),
		PublicKey: []byte("public key"), RawFlags: 0x01, UserPresent: true, CreatedAt: time.Now(),
	}
	if err := w.db.Accounts().CreatePasskey(t.Context(), cred); err != nil {
		t.Fatal(err)
	}
	return cred.ID
}

// save saves text from the snapshot in force.
func (w *world) save(t *testing.T, text string) ([]formschema.FieldError, error) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	_, errs, err := w.svc.Save(t.Context(), doc.Content[0], w.svc.Snapshot().Revision())
	return errs, err
}

func (w *world) record(t *testing.T) (statedb.AuthMethodState, bool) {
	t.Helper()
	rec, ok, err := statedb.MethodStateOf(t.Context(), w.db.Accounts())
	if err != nil {
		t.Fatal(err)
	}
	return rec, ok
}

func (w *world) file(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(w.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return b
}

// refused checks a Save of text answers one field error at path reading the
// count, with the file and the record unchanged.
func (w *world) refused(t *testing.T, text, path string, n int) {
	t.Helper()
	file, rec := w.file(t), w.mustRecord(t)
	errs, err := w.save(t, text)
	want := "would leave 1 user with no way to sign in"
	if n != 1 {
		want = fmt.Sprintf("would leave %d users with no way to sign in", n)
	}
	if err != nil || len(errs) != 1 || errs[0].Path != path || errs[0].Message != want {
		t.Errorf("Save = %v, %v; want one field error at %s reading %q", errs, err, path, want)
	}
	if !bytes.Equal(w.file(t), file) {
		t.Error("a refused Save changed the file")
	}
	if got := w.mustRecord(t); got != rec {
		t.Errorf("a refused Save changed the record: %+v, want %+v", got, rec)
	}
}

func (w *world) mustRecord(t *testing.T) statedb.AuthMethodState {
	t.Helper()
	rec, ok := w.record(t)
	if !ok {
		t.Fatal("no record stored")
	}
	return rec
}

// commits checks a Save of text commits with no field error.
func (w *world) commits(t *testing.T, text string) {
	t.Helper()
	if errs, err := w.save(t, text); err != nil || len(errs) != 0 {
		t.Errorf("Save = %v, %v; want it committed", errs, err)
	}
}

func TestMethodGuardRefusesStranding(t *testing.T) {
	t.Run("a password-only admin keeps password sign-in until linked", func(t *testing.T) {
		w := newWorld(t, languages)
		alice := w.user(t, "alice")
		w.refused(t, languages+"auth:\n  password_login: false\n", "auth.password_login", 1)
		w.link(t, alice, issuerA)
		w.commits(t, languages+"auth:\n"+oidcOn(issuerA))
		w.commits(t, languages+"auth:\n  password_login: false\n"+oidcOn(issuerA))
		if rec := w.mustRecord(t); rec.PasswordLogin || rec.OIDCIssuer != issuerA {
			t.Errorf("record = %+v, want password off and OIDC at %s", rec, issuerA)
		}
	})
	t.Run("passkeys alone carry every user with OIDC off", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+passkeysAt(rpA))
		bob, carol := w.user(t, "bob"), w.user(t, "carol")
		w.passkey(t, bob, rpA)
		w.passkey(t, carol, rpA)
		w.dropPassword(t, bob)
		w.commits(t, languages+"auth:\n  password_login: false\n"+passkeysAt(rpA))
	})
	t.Run("a passkey-only user keeps the RP ID", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+passkeysAt(rpA))
		dave := w.user(t, "dave")
		w.passkey(t, dave, rpA)
		w.dropPassword(t, dave)
		w.refused(t, languages+"auth:\n"+passkeysAt(rpB), "auth.passkey_origins", 1)
	})
	t.Run("an OIDC-only user keeps OIDC and its issuer until it holds a password", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+oidcOn(issuerA))
		erin := w.user(t, "erin")
		w.link(t, erin, issuerA)
		hash := func() access.Hash {
			u, _, err := w.db.Accounts().UserByID(t.Context(), erin.ID)
			if err != nil {
				t.Fatal(err)
			}
			return u.PasswordHash
		}()
		w.dropPassword(t, erin)
		w.refused(t, languages, "auth.oidc_enabled", 1)
		w.refused(t, languages+"auth:\n"+oidcOn(issuerB), "auth.oidc_issuer", 1)
		w.edit(t, erin, func(u *access.User) { u.PasswordHash = hash })
		w.commits(t, languages+"auth:\n"+oidcOn(issuerB))
		w.commits(t, languages)
	})
	t.Run("one Save turning OIDC on and password off for linked users", func(t *testing.T) {
		w := newWorld(t, languages)
		for _, n := range []string{"fay", "gus"} {
			w.link(t, w.user(t, n), issuerA)
		}
		w.commits(t, languages+"auth:\n  password_login: false\n"+oidcOn(issuerA))
	})
	t.Run("an already stranded user refuses nothing", func(t *testing.T) {
		w := newWorld(t, languages)
		w.dropPassword(t, w.user(t, "hal"))
		w.commits(t, languages+"auth:\n  session_idle_timeout: 1h\n")
		w.commits(t, languages+"auth:\n  session_idle_timeout: 1h\n  password_login: false\n")
	})
	t.Run("a change keeping every method rewrites the record", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+passkeysAt(rpA)+oidcOn(issuerA))
		ivy := w.user(t, "ivy")
		w.passkey(t, ivy, rpA)
		w.link(t, ivy, issuerA)
		w.dropPassword(t, ivy)
		before := w.mustRecord(t)
		w.commits(t, languages+"auth:\n  passkey_origins: [https://nas.example, https://app.nas.example]\n"+oidcOn(issuerA))
		w.commits(t, languages+"auth:\n  passkey_origins: [https://nas.example, https://app.nas.example]\n"+
			"  oidc_enabled: true\n  oidc_issuer: "+issuerA+"\n  oidc_client_id: another\n  oidc_client_secret: s3cret\n  oidc_redirect_url: https://nas.example/api/auth/oidc/callback\n")
		after := w.mustRecord(t)
		if after.SignIn.OIDCClientID != "another" || after.SignIn.PasskeyOrigins == before.SignIn.PasskeyOrigins || after.RPID != rpA {
			t.Errorf("record after the two Saves = %+v, want the new client id and origin set under RP ID %s", after, rpA)
		}
	})
	t.Run("a Failed file's repair Save is judged against the stored record", func(t *testing.T) {
		w := newWorld(t, "auth:\n  password_login: true\nlanguages: 7\n")
		if !w.mustRecord(t).PasswordLogin {
			t.Fatal("the Failed boot's auth-only activation did not record password on")
		}
		w.user(t, "jan")
		w.refused(t, languages+"auth:\n  password_login: false\n", "auth.password_login", 1)
	})
	t.Run("an Unconfigured store's first Save is judged against the defaults", func(t *testing.T) {
		w := newWorld(t, "")
		if rec, ok := w.record(t); !ok || rec != accounts.MethodRecord(accounts.Config{}) {
			t.Fatalf("record after an Unconfigured boot = %+v, %v; want the defaults'", rec, ok)
		}
		w.user(t, "kim")
		w.refused(t, languages+"auth:\n  password_login: false\n", "auth.password_login", 1)
	})
	t.Run("a guard read fault and a closed account service abort the Update", func(t *testing.T) {
		for name, fault := range map[string]func(w *world){
			"store fault": func(w *world) {
				w.faultTx = func(tx account.Tx) account.Tx { return listFault{tx} }
				w.precheckFault = true
			},
			"service closed": func(w *world) { w.stop() },
		} {
			t.Run(name, func(t *testing.T) {
				w := newWorld(t, languages)
				w.user(t, "lou")
				file, rec := w.file(t), w.mustRecord(t)
				fault(w)
				if errs, err := w.save(t, languages+"auth:\n  password_login: false\n"); !errors.Is(err, settings.ErrActivation) || len(errs) != 0 {
					t.Errorf("Save = %v, %v; want ErrActivation", errs, err)
				}
				if !bytes.Equal(w.file(t), file) || w.mustRecord(t) != rec {
					t.Error("the aborted Save left the file or the record changed")
				}
			})
		}
	})
	t.Run("the pre-check refuses before the Update and steps aside on a read fault", func(t *testing.T) {
		w := newWorld(t, languages)
		w.user(t, "max")
		n := w.activations.Load()
		w.refused(t, languages+"auth:\n  password_login: false\n", "auth.password_login", 1)
		if got := w.activations.Load() - n; got != 0 {
			t.Errorf("activations run for a pre-check refusal = %d, want 0", got)
		}
		w.precheckFault = true
		w.refused(t, languages+"auth:\n  password_login: false\n", "auth.password_login", 1)
		if got := w.activations.Load() - n; got != 1 {
			t.Errorf("activations run with the pre-check's read failing = %d, want 1: the guard decides", got)
		}
	})
}

// signIn signs name in with its password from loopback and answers the
// session's principal.
func (w *world) signIn(t *testing.T, name string) access.Principal {
	t.Helper()
	loopback := netip.MustParseAddr("127.0.0.1")
	in, err := w.acct.PasswordLogin(t.Context(), account.Client{Peer: loopback, Via: access.ViaDirect}, account.Credentials{Username: name, Password: access.NewSecret(password)})
	if err != nil {
		t.Fatalf("sign in %s: %v", name, err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8374/api/session", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	cookie := w.s.Authenticator().Cookie()
	r.AddCookie(&http.Cookie{Name: cookie.CookieName(r), Value: in.Token.Reveal()})
	p, err := w.s.Authenticator().Authenticate(r)
	if err != nil {
		t.Fatalf("authenticate %s: %v", name, err)
	}
	return p
}

func (w *world) passkeys(t *testing.T, u *access.User) int {
	t.Helper()
	ps, err := w.db.Accounts().PasskeysByUserID(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return len(ps)
}

func TestMethodGuardRefusesStrandingRaces(t *testing.T) {
	const passwordOff = languages + "auth:\n  password_login: false\n" + "  passkey_origins: [https://" + rpA + "]\n"
	t.Run("a removal between the pre-check and the Update", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+passkeysAt(rpA))
		nia := w.user(t, "nia")
		pk := w.passkey(t, nia, rpA)
		p := w.signIn(t, "nia")
		w.beforeActivate = func() {
			w.beforeActivate = nil
			if err := w.acct.RemovePasskey(t.Context(), p, pk); err != nil {
				t.Errorf("RemovePasskey before the Update = %v, want it committed", err)
			}
		}
		w.refused(t, passwordOff, "auth.password_login", 1)
		if !w.mustRecord(t).PasswordLogin {
			t.Error("nia lost password sign-in")
		}
	})
	t.Run("a removal blocked behind a committed Save", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+passkeysAt(rpA))
		oli := w.user(t, "oli")
		pk := w.passkey(t, oli, rpA)
		p := w.signIn(t, "oli")
		removed := make(chan error, 1)
		w.inUpdate = func() {
			w.inUpdate = nil
			go func() { removed <- w.acct.RemovePasskey(context.WithoutCancel(t.Context()), p, pk) }()
		}
		w.commits(t, passwordOff)
		if err := <-removed; !errors.Is(err, account.ErrLastMethod) {
			t.Errorf("RemovePasskey behind the Save = %v, want ErrLastMethod", err)
		}
		if w.passkeys(t, oli) != 1 {
			t.Error("oli lost the last passkey")
		}
	})
}

var errNotRun = errors.New("the removal did not run")

func (w *world) removePassword(t *testing.T, p access.Principal) error {
	t.Helper()
	loopback := netip.MustParseAddr("127.0.0.1")
	_, err := w.acct.RemovePassword(t.Context(), account.Client{Peer: loopback, Via: access.ViaDirect}, p, access.NewSecret(password))
	return err
}

func (w *world) stored(t *testing.T, u *access.User) *access.User {
	t.Helper()
	got, _, err := w.db.Accounts().UserByID(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestMethodGuardRefusesStrandingAroundPublish races a removal with a Save
// that has committed its record and not yet published its snapshot, the
// window in which the record names the new RP ID or issuer and sign-in still
// runs on the published one, and with the same Save before its Update.
func TestMethodGuardRefusesStrandingAroundPublish(t *testing.T) {
	rpSave := languages + "auth:\n" + passkeysAt(rpB)
	t.Run("a passkey removed inside the RP ID window", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+passkeysAt(rpA))
		pia := w.user(t, "pia")
		w.passkey(t, pia, rpA)
		p2 := w.passkey(t, pia, rpB)
		p := w.signIn(t, "pia")
		w.dropPassword(t, pia)
		removed := errNotRun
		w.afterActivate = func() {
			w.afterActivate = nil
			removed = w.acct.RemovePasskey(t.Context(), p, p2)
		}
		w.commits(t, rpSave)
		if !errors.Is(removed, account.ErrLastMethod) {
			t.Errorf("RemovePasskey inside the window = %v, want ErrLastMethod", removed)
		}
		if n := w.passkeys(t, pia); n != 2 || w.mustRecord(t).RPID != rpB {
			t.Errorf("after the publish pia holds %d passkeys under record %+v, want both under %s", n, w.mustRecord(t), rpB)
		}
	})
	t.Run("a passkey removed before the RP ID Save's Update", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+passkeysAt(rpA))
		ray := w.user(t, "ray")
		w.passkey(t, ray, rpA)
		p2 := w.passkey(t, ray, rpB)
		p := w.signIn(t, "ray")
		w.dropPassword(t, ray)
		w.beforeActivate = func() {
			w.beforeActivate = nil
			if err := w.acct.RemovePasskey(t.Context(), p, p2); err != nil {
				t.Errorf("RemovePasskey before the Update = %v, want it committed", err)
			}
		}
		w.refused(t, rpSave, "auth.passkey_origins", 1)
		if n := w.passkeys(t, ray); n != 1 || w.mustRecord(t).RPID != rpA {
			t.Errorf("ray holds %d passkeys under record %+v, want the one under %s", n, w.mustRecord(t), rpA)
		}
	})
	i1 := accesstest.NewIdP(t, accesstest.IdPOptions{})
	i2 := accesstest.NewIdP(t, accesstest.IdPOptions{})
	issuerSave := languages + "auth:\n" + oidcOn(i2.Issuer())
	t.Run("a password removed inside the issuer window", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+oidcOn(i1.Issuer()))
		sam, tia := w.user(t, "sam"), w.user(t, "tia")
		w.link(t, sam, i1.Issuer())
		w.link(t, tia, i2.Issuer())
		ps, pt := w.signIn(t, "sam"), w.signIn(t, "tia")
		removedS, removedT := errNotRun, errNotRun
		w.afterActivate = func() {
			w.afterActivate = nil
			removedS, removedT = w.removePassword(t, ps), w.removePassword(t, pt)
		}
		w.commits(t, issuerSave)
		if !errors.Is(removedS, account.ErrLastMethod) {
			t.Errorf("sam's RemovePassword inside the window = %v, want ErrLastMethod: the link is not the record's issuer", removedS)
		}
		if !errors.Is(removedT, account.ErrLastMethod) {
			t.Errorf("tia's RemovePassword inside the window = %v, want ErrLastMethod: the link is not the published issuer", removedT)
		}
		for _, u := range []*access.User{sam, tia} {
			if w.stored(t, u).PasswordHash.IsZero() {
				t.Errorf("%s lost the password", u.Username)
			}
		}
	})
	t.Run("a password removed before the issuer Save's Update", func(t *testing.T) {
		w := newWorld(t, languages+"auth:\n"+oidcOn(i1.Issuer()))
		uma := w.user(t, "uma")
		w.link(t, uma, i1.Issuer())
		p := w.signIn(t, "uma")
		w.beforeActivate = func() {
			w.beforeActivate = nil
			if err := w.removePassword(t, p); err != nil {
				t.Errorf("RemovePassword before the Update = %v, want it committed", err)
			}
		}
		w.refused(t, issuerSave, "auth.oidc_issuer", 1)
		if got := w.stored(t, uma).OIDC.Issuer; got != i1.Issuer() || w.mustRecord(t).OIDCIssuer != i1.Issuer() {
			t.Errorf("uma's link = %q under record %+v, want %q kept", got, w.mustRecord(t), i1.Issuer())
		}
	})
}

// listFault fails every user listing.
type listFault struct{ account.Tx }

func (listFault) ListUsers(context.Context) ([]access.User, error) {
	return nil, errors.New("planted list fault")
}
