package accounts

import (
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/cplieger/access/webauthn"
)

// newRelyingParty is the relying party build, a seam for the build-error
// test.
var newRelyingParty = webauthn.New

// relyingParties is account's RP getter: one relying party per distinct
// passkey_origins set, reused until the set changes.
type relyingParties struct {
	rp     *webauthn.RelyingParty
	logger *slog.Logger
	key    string
	mu     sync.Mutex
	built  bool
}

// get answers c's relying party, nil while passkeys are off. ParseConfig
// built one from the same origins, so a build error here is a programming
// fault: it logs ERROR once for that set and turns passkeys off.
func (p *relyingParties) get(c Config) *webauthn.RelyingParty {
	cfg := c.get()
	if cfg.rpID == "" {
		return nil
	}
	key := strings.Join(slices.Sorted(slices.Values(cfg.origins)), "\n")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.built && p.key == key {
		return p.rp
	}
	rp, err := newRelyingParty(rpConfig(cfg.rpID, cfg.origins))
	if err != nil {
		p.logger.Error("accounts: the passkey relying party did not build; passkeys are off", "rp_id", cfg.rpID, "error", err)
		rp = nil
	}
	p.rp, p.key, p.built = rp, key, true
	return rp
}

// id is the published RP ID, empty while passkeys are off.
func (p *relyingParties) id(c Config) string {
	if rp := p.get(c); rp != nil {
		return rp.ID()
	}
	return ""
}

// rpConfig is the one relying party shape: ParseConfig refuses origins it
// does not build, and get builds it.
func rpConfig(id string, origins []string) webauthn.RPConfig {
	return webauthn.RPConfig{ID: id, DisplayName: "subflux", Origins: origins}
}
