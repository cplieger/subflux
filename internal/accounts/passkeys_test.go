package accounts

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesshttp"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/webauthn"
	"github.com/cplieger/go/slogx/capture"
)

// passkeySite is a site whose passkeys run at origins.
func passkeySite(t *testing.T, e *env, origins ...string) *site {
	t.Helper()
	e.auth = parsed(t, map[string]any{keyPasskeyOrigins: origins})
	st := newSite(t, e, accesstest.NewMemStore(), nil)
	createUser(t, st.s, "admin", access.RoleAdmin, testPassword)
	return st
}

// ceremony begins the named WebAuthn route from b.
func (b *browser) ceremony(t *testing.T, st *site, name string) (accesshttp.CeremonyResponse, int, string) {
	t.Helper()
	w := b.call(t, st, name, nil)
	var c accesshttp.CeremonyResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
	}
	return c, w.Code, codeOf(t, w)
}

// finish sends body to the named finish route for ceremony c.
func (b *browser) finish(t *testing.T, st *site, name string, c accesshttp.CeremonyResponse, body []byte) (int, string) {
	t.Helper()
	w := b.sendWith(st, http.MethodPost, st.path(t, name), string(body), http.Header{"X-Auth-Ceremony": {c.Ceremony}})
	return w.Code, codeOf(t, w)
}

// enrol registers dev as a passkey of the account b is signed in to.
func (b *browser) enrol(t *testing.T, st *site, dev *accesstest.PasskeyDevice) {
	t.Helper()
	c, code, answer := b.ceremony(t, st, "authPasskeyRegisterBegin")
	if code != http.StatusOK {
		t.Fatalf("register begin = %d %s", code, answer)
	}
	if code, answer := b.finish(t, st, "authPasskeyRegisterFinish", c, dev.Register(c.PublicKey)); code != http.StatusCreated {
		t.Fatalf("register finish = %d %s", code, answer)
	}
}

func TestRelyingPartyBuiltOncePerOrigins(t *testing.T) {
	var builds atomic.Int64
	swap(t, &newRelyingParty, func(c webauthn.RPConfig) (*webauthn.RelyingParty, error) {
		builds.Add(1)
		return webauthn.New(c)
	})
	e := newEnv()
	logger, rec := capture.New()
	e.auth = parsed(t, map[string]any{keyPasskeyOrigins: []string{siteOrigin}})
	st := newSite(t, e, accesstest.NewMemStore(), func(d *Deps) { d.Logger = logger })
	b := newBrowser(t, siteOrigin, "192.0.2.10")
	begin := func(t *testing.T) (int, string) {
		t.Helper()
		_, code, answer := b.ceremony(t, st, "authLoginPasskeyBegin")
		return code, answer
	}
	builds.Store(0)
	for range 2 {
		if code, answer := begin(t); code != http.StatusOK {
			t.Fatalf("passkey begin = %d %s", code, answer)
		}
	}
	if got := builds.Load(); got != 1 {
		t.Errorf("relying parties built for two ceremonies under one origin set = %d, want 1", got)
	}
	e.set(func(e *env) {
		e.auth = parsed(t, map[string]any{keyPasskeyOrigins: []string{"https://app.nas.example", siteOrigin}})
	})
	if code, answer := begin(t); code != http.StatusOK {
		t.Fatalf("passkey begin after the change = %d %s", code, answer)
	}
	if got := builds.Load(); got != 2 {
		t.Errorf("relying parties built after an origin change = %d, want 2", got)
	}

	t.Run("a build error turns passkeys off", func(t *testing.T) {
		swap(t, &newRelyingParty, func(webauthn.RPConfig) (*webauthn.RelyingParty, error) {
			return nil, errors.New("planted build failure")
		})
		e.set(func(e *env) {
			e.auth = parsed(t, map[string]any{keyPasskeyOrigins: []string{"https://media.nas.example", siteOrigin}})
		})
		if code, answer := begin(t); code != http.StatusBadRequest || answer != string(accesshttp.CodePasskeysUnavailable) {
			t.Errorf("passkey begin after a build error = %d %s, want 400 %s", code, answer, accesshttp.CodePasskeysUnavailable)
		}
		var logged bool
		for _, r := range rec.Records() {
			logged = logged || (r.Level == slog.LevelError && strings.Contains(r.Message, "relying party did not build"))
		}
		if !logged {
			t.Error("a build error logged no ERROR line")
		}
	})
}

func TestPasskeySiblingOriginRefused(t *testing.T) {
	const sibling = "https://other.nas.example"
	st := passkeySite(t, newEnv(), siteOrigin)
	owner := newBrowser(t, siteOrigin, "127.0.0.1")
	owner.signInPassword(t, st, "admin")
	dev := accesstest.NewPasskeyDevice(t)
	owner.enrol(t, st, dev)

	t.Run("a ceremony begun at a sibling origin", func(t *testing.T) {
		_, code, answer := newBrowser(t, sibling, "192.0.2.10").ceremony(t, st, "authLoginPasskeyBegin")
		if code != http.StatusBadRequest || answer != string(accesshttp.CodeOriginRefused) {
			t.Errorf("begin from %s = %d %s, want 400 %s", sibling, code, answer, accesshttp.CodeOriginRefused)
		}
	})
	t.Run("an assertion signed for a sibling origin", func(t *testing.T) {
		b := newBrowser(t, siteOrigin, "192.0.2.11")
		c, code, answer := b.ceremony(t, st, "authLoginPasskeyBegin")
		if code != http.StatusOK {
			t.Fatalf("begin = %d %s", code, answer)
		}
		dev.Origin = sibling
		if code, answer := b.finish(t, st, "authLoginPasskeyFinish", c, dev.Assert(c.PublicKey)); code != http.StatusUnauthorized || answer != string(accesshttp.CodeAssertionFailed) {
			t.Errorf("finish relayed from %s = %d %s, want 401 %s", sibling, code, answer, accesshttp.CodeAssertionFailed)
		}
		if hasSession(b) {
			t.Error("a relayed assertion signed in")
		}
	})
	t.Run("the listed origin still signs in", func(t *testing.T) {
		b := newBrowser(t, siteOrigin, "192.0.2.12")
		c, _, _ := b.ceremony(t, st, "authLoginPasskeyBegin")
		dev.Origin = siteOrigin
		if code, answer := b.finish(t, st, "authLoginPasskeyFinish", c, dev.Assert(c.PublicKey)); code != http.StatusOK {
			t.Errorf("finish at %s = %d %s, want 200", siteOrigin, code, answer)
		}
	})
}

func TestPasskeyFinishBodyCap(t *testing.T) {
	st := passkeySite(t, newEnv(), siteOrigin)
	b := newBrowser(t, siteOrigin, "192.0.2.10")
	c, code, answer := b.ceremony(t, st, "authLoginPasskeyBegin")
	if code != http.StatusOK {
		t.Fatalf("begin = %d %s", code, answer)
	}
	body := []byte(`{"id":"` + strings.Repeat("A", 65<<10) + `"}`)
	if code, answer := b.finish(t, st, "authLoginPasskeyFinish", c, body); code != http.StatusRequestEntityTooLarge || answer != string(accesshttp.CodeBodyTooLarge) {
		t.Errorf("a 65 KiB finish = %d %s, want 413 %s", code, answer, accesshttp.CodeBodyTooLarge)
	}
}
