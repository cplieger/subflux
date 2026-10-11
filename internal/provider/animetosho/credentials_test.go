package animetosho

import (
	"errors"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/provider/anidb"
	"github.com/cplieger/subflux/internal/subflux"
)

// AnimeTosho's only credential is the optional AniDB client key, so the check
// delegates whole. The delegation is what this pins; the three outcomes live
// with the key's owner, in anidb's own suite.
//
// The absent-key arm is the one a real operator meets: AnimeTosho searches by
// title without a key, so the answer has to say there is nothing to validate
// rather than claim a working setup is broken.
func TestCheckCredentials_delegates_to_the_anidb_client_key(t *testing.T) {
	t.Parallel()
	p := &source{anidbMapper: anidb.NewMapper("")}

	err := p.CheckCredentials(t.Context())

	authErr, refused := errors.AsType[*subflux.AuthError](err)
	if !refused {
		t.Fatalf("CheckCredentials() with no client key = %v, want a *subflux.AuthError", err)
	}
	if !strings.Contains(authErr.Msg, "client key") {
		t.Errorf("CheckCredentials() message = %q, want it to name the client key", authErr.Msg)
	}
}
