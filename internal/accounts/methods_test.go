package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/account"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/statedb"
)

func parsed(t *testing.T, kv map[string]any) Config {
	t.Helper()
	c, errs := ParseConfig(section(kv))
	if errs != nil {
		t.Fatalf("%v: %v", kv, errs)
	}
	return c
}

// digestOf is statedb.SetDigest's rule written out for values already
// distinct and sorted.
func digestOf(values ...string) statedb.SetDigest {
	h := sha256.New()
	for _, v := range values {
		h.Write(binary.BigEndian.AppendUint32(nil, uint32(len(v))))
		h.Write([]byte(v))
	}
	d := statedb.SetDigest{Count: len(values)}
	h.Sum(d.SHA256[:0])
	return d
}

func TestMethodRecordFields(t *testing.T) {
	base := withOIDC(map[string]any{
		keyPasskeyOrigins: []string{"https://nas.example", "https://app.nas.example"},
		keyAllowedGroups:  []string{"media", "admins"},
	})
	rec := MethodRecord(parsed(t, base))
	t.Run("the record of a full section", func(t *testing.T) {
		want := statedb.AuthMethodState{
			PasswordLogin: true,
			OIDCIssuer:    "https://idp.example/realm",
			RPID:          "nas.example",
			SignIn: statedb.SignInKeys{
				OIDCClientID:      "subflux",
				OIDCRedirectURL:   "https://nas.example" + callbackPath,
				OIDCAllowedGroups: digestOf("admins", "media"),
				PasskeyOrigins:    digestOf("https://app.nas.example", "https://nas.example"),
				OIDCClientSecret:  sha256.Sum256([]byte("s3cret")),
			},
		}
		if rec != want {
			t.Errorf("MethodRecord = %+v, want %+v", rec, want)
		}
	})
	t.Run("no byte of the client secret is in the record", func(t *testing.T) {
		if strings.Contains(fmt.Sprintf("%+v %#v", rec, rec), "s3cret") {
			t.Error("the record holds the client secret")
		}
	})
	t.Run("the defaults' record", func(t *testing.T) {
		want := statedb.AuthMethodState{PasswordLogin: true, SignIn: statedb.SignInKeys{OIDCAllowedGroups: digestOf(), PasskeyOrigins: digestOf()}}
		if got := MethodRecord(Config{}); got != want {
			t.Errorf("MethodRecord(Config{}) = %+v, want %+v", got, want)
		}
	})

	changes := []struct {
		key   string
		value any
		field func(*statedb.AuthMethodState)
	}{
		{keyPasswordLogin, "false", func(s *statedb.AuthMethodState) { s.PasswordLogin = false }},
		{keyOIDCEnabled, "false", func(s *statedb.AuthMethodState) { s.OIDCIssuer = "" }},
		{keyOIDCIssuer, "https://other.example", func(s *statedb.AuthMethodState) { s.OIDCIssuer = "https://other.example" }},
		{keyPasskeyOrigins, []string{"https://other.example"}, func(s *statedb.AuthMethodState) {
			s.RPID, s.SignIn.PasskeyOrigins = "other.example", digestOf("https://other.example")
		}},
		{keyOIDCClientID, "other", func(s *statedb.AuthMethodState) { s.SignIn.OIDCClientID = "other" }},
		{keyOIDCRedirect, "https://other.example" + callbackPath, func(s *statedb.AuthMethodState) {
			s.SignIn.OIDCRedirectURL = "https://other.example" + callbackPath
		}},
		{keyAutoProvision, "true", func(s *statedb.AuthMethodState) { s.SignIn.OIDCAutoProvision = true }},
		{keyAllowedGroups, []string{"media"}, func(s *statedb.AuthMethodState) { s.SignIn.OIDCAllowedGroups = digestOf("media") }},
		{keyDisableAuth, "true", func(s *statedb.AuthMethodState) { s.SignIn.DisableAuth = true }},
		{keyOIDCSecret, "other", func(s *statedb.AuthMethodState) { s.SignIn.OIDCClientSecret = sha256.Sum256([]byte("other")) }},
	}
	for _, c := range changes {
		t.Run(c.key+" changed alone changes its field", func(t *testing.T) {
			kv := withOIDC(nil)
			maps.Copy(kv, base)
			kv[c.key] = c.value
			want := rec
			c.field(&want)
			if got := MethodRecord(parsed(t, kv)); got != want {
				t.Errorf("MethodRecord = %+v, want %+v", got, want)
			}
		})
	}

	t.Run("a reorder or a duplicate keeps the set digests", func(t *testing.T) {
		kv := withOIDC(map[string]any{
			keyPasskeyOrigins: []string{"https://app.nas.example", "https://nas.example", "https://NAS.example:443"},
			keyAllowedGroups:  []string{"admins", "media", "admins"},
		})
		if got := MethodRecord(parsed(t, kv)); got != rec {
			t.Errorf("MethodRecord = %+v, want %+v", got, rec)
		}
	})
	t.Run("an empty secret records zero", func(t *testing.T) {
		kv := withOIDC(map[string]any{keyOIDCEnabled: "false", keyOIDCSecret: ""})
		if got := MethodRecord(parsed(t, kv)).SignIn.OIDCClientSecret; got != [32]byte{} {
			t.Errorf("secret digest %x, want zero", got)
		}
	})
}

// published is the RP ID the test services publish; moved is another.
const (
	published = "nas.example"
	moved     = "media.example"
)

// recordService is a store-backed service publishing RP ID published, with
// rec stored as table 2B's record unless rec is nil.
func recordService(t *testing.T, rec *statedb.AuthMethodState) (*Service, *statedb.Store) {
	t.Helper()
	e := newEnv()
	e.auth = parsed(t, map[string]any{keyPasskeyOrigins: []string{"https://" + published}})
	s, st := storeService(t, e)
	if rec != nil {
		if err := st.ActivateAuth(t.Context(), *rec, func(context.Context, account.Tx, statedb.AuthMethodState, bool, statedb.AuthMethodState) error {
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return s, st
}

func addPasskey(t *testing.T, st *statedb.Store, userID int64, rpID string) int64 {
	t.Helper()
	cred := &access.PasskeyCredential{
		UserID: userID, RPID: rpID, Name: rpID, CredentialID: []byte(fmt.Sprintf("cred-%d-%s", userID, rpID)),
		PublicKey: []byte("public key"), RawFlags: 0x01, UserPresent: true, CreatedAt: time.Now(),
	}
	if err := st.Accounts().CreatePasskey(t.Context(), cred); err != nil {
		t.Fatal(err)
	}
	return cred.ID
}

// dropPassword clears u's password in the store, keeping its sessions.
func dropPassword(t *testing.T, st *statedb.Store, u *access.User) {
	t.Helper()
	got, _, err := st.Accounts().UserByID(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.PasswordHash = access.Hash{}
	if err := st.Accounts().UpdateUser(t.Context(), got); err != nil {
		t.Fatal(err)
	}
}

func TestMethodUsableReadsRecord(t *testing.T) {
	t.Run("a record with password off leaves the passkey last", func(t *testing.T) {
		s, st := recordService(t, &statedb.AuthMethodState{RPID: published})
		u := createUser(t, s, "alice", access.RoleAdmin, testPassword)
		pk := addPasskey(t, st, u.ID, published)
		if err := s.account.RemovePasskey(t.Context(), signIn(t, s, "alice"), pk); !errors.Is(err, account.ErrLastMethod) {
			t.Errorf("RemovePasskey = %v, want ErrLastMethod: the password the record turns off counted", err)
		}
	})
	t.Run("a moved record RP ID counts no passkey", func(t *testing.T) {
		s, st := recordService(t, &statedb.AuthMethodState{PasswordLogin: true, RPID: moved})
		u := createUser(t, s, "alice", access.RoleAdmin, testPassword)
		addPasskey(t, st, u.ID, published)
		if _, err := s.account.RemovePassword(t.Context(), clientAt("127.0.0.1"), signIn(t, s, "alice"), access.NewSecret(testPassword)); !errors.Is(err, account.ErrLastMethod) {
			t.Errorf("RemovePassword = %v, want ErrLastMethod: a passkey under the published RP ID counted while the record names another", err)
		}
	})
	t.Run("a committed RP ID move awaiting publication counts no passkey", func(t *testing.T) {
		s, st := recordService(t, &statedb.AuthMethodState{PasswordLogin: true, RPID: moved})
		u := createUser(t, s, "alice", access.RoleAdmin, testPassword)
		p := signIn(t, s, "alice")
		p1 := addPasskey(t, st, u.ID, moved)
		addPasskey(t, st, u.ID, published)
		dropPassword(t, st, u)
		if err := s.account.RemovePasskey(t.Context(), p, p1); !errors.Is(err, account.ErrLastMethod) {
			t.Errorf("RemovePasskey(P1) = %v, want ErrLastMethod: P2 counted under the published RP ID the record has left", err)
		}
	})
	t.Run("no record counts every held method", func(t *testing.T) {
		s, st := recordService(t, nil)
		u := createUser(t, s, "alice", access.RoleAdmin, testPassword)
		pk := addPasskey(t, st, u.ID, published)
		if err := s.account.RemovePasskey(t.Context(), signIn(t, s, "alice"), pk); err != nil {
			t.Errorf("RemovePasskey = %v, want the password to count with no record", err)
		}
	})
	t.Run("another store's tx counts nothing", func(t *testing.T) {
		usable := methodUsable(func() string { return published })
		u := &access.User{ID: 1, OIDC: access.OIDCIdentity{Issuer: "https://idp.example", Subject: "a"}}
		for _, m := range []access.Method{access.MethodPassword, access.MethodPasskey, access.MethodOIDC} {
			if usable(t.Context(), accesstest.NewMemStore(), u, m) {
				t.Errorf("%s usable through a foreign tx", m)
			}
		}
	})
}

// listCounting counts the user listings read through it.
type listCounting struct {
	account.Tx
	n *atomic.Int64
}

func (l listCounting) ListUsers(ctx context.Context) ([]access.User, error) {
	l.n.Add(1)
	return l.Tx.ListUsers(ctx)
}

func TestMethodGuardReadsNoUserUnlessAMethodGoes(t *testing.T) {
	s, store := memService(t, newEnv())
	createUser(t, s, "alice", access.RoleAdmin, testPassword)
	base := withOIDC(map[string]any{keyPasskeyOrigins: []string{"https://nas.example"}})
	before := MethodRecord(parsed(t, base))
	reads := func(t *testing.T, over map[string]any) int64 {
		t.Helper()
		kv := maps.Clone(base)
		maps.Copy(kv, over)
		next := MethodRecord(parsed(t, kv))
		n := &atomic.Int64{}
		if err := store.Update(t.Context(), func(tx account.Tx) error {
			return s.MethodGuard()(t.Context(), listCounting{Tx: tx, n: n}, before, true, next)
		}); err != nil && !errors.As(err, new(*formschema.RefusedError)) {
			t.Fatalf("guard: %v", err)
		}
		return n.Load()
	}
	for name, over := range map[string]map[string]any{
		"no method key changed":           {keyIdleTimeout: "1h"},
		"an origin added under the RP ID": {keyPasskeyOrigins: []string{"https://nas.example", "https://app.nas.example"}},
		"the client id changed":           {keyOIDCClientID: "another"},
	} {
		if got := reads(t, over); got != 0 {
			t.Errorf("%s: user listings = %d, want 0", name, got)
		}
	}
	if got := reads(t, map[string]any{keyPasswordLogin: no}); got != 1 {
		t.Errorf("password off: user listings = %d, want 1, so the zero counts above prove something", got)
	}
}
