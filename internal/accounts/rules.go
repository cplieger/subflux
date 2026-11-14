package accounts

import (
	"context"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/access/oidc"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/statedb"
)

// The key scopes subflux's API keys carry. manage_users and create_keys are
// subflux's own (DECISIONS 31 and 36): distinct from admin and each other,
// and implied by no scope, full access included.
const (
	scopeRead        = "read"
	scopeOperate     = "operate"
	scopeAdmin       = "admin"
	scopeManageUsers = "manage_users"
	scopeCreateKeys  = "create_keys"
)

// groupsClaim is the OIDC claim oidc_allowed_groups is matched against.
const groupsClaim = "groups"

// rulesOf is the account.Rules c sets: every field section 7.2 names, setup
// the token rule and MethodUsable methodUsable over the published RP ID.
func rulesOf(c Config, setup func(context.Context, account.Client) bool, rpID func() string) account.Rules {
	cfg := c.get()
	breach := account.BreachOff
	if cfg.breach {
		breach = account.BreachFailOpen
	}
	var prov oidc.Provisioning
	if cfg.autoProvision {
		prov = oidc.Provisioning{Enabled: true, Claim: groupsClaim, AllowedValues: cfg.groups}
	}
	return account.Rules{
		DisablePasswordLogin:         !cfg.passwordLogin,
		SetupTokenRequired:           setup,
		MethodUsable:                 methodUsable(rpID),
		RecentAuth:                   10 * time.Minute,
		Breach:                       breach,
		Provisioning:                 prov,
		ForbiddenWords:               []string{"subflux"},
		Scopes:                       []string{scopeRead, scopeOperate, scopeAdmin, scopeManageUsers, scopeCreateKeys},
		KeyPrefix:                    "sfx_",
		MaxSessionsPerUser:           50,
		SSOGoverned:                  true,
		RevokeKeysOnCredentialChange: true,
	}
}

// timeouts is c's session timeouts.
func (c Config) timeouts() access.SessionTimeouts {
	cfg := c.get()
	return access.SessionTimeouts{Idle: cfg.idle, Absolute: cfg.absolute}
}

// setupTokenRequired is Rules.SetupTokenRequired: SetupLocality over s.local
// (clientaddr.Local and Deps.Forwarder) and allowed_hosts, asking every client
// for the token while the server section failed, since its host list may be
// incomplete.
func (s *Service) setupTokenRequired(ctx context.Context, c account.Client) bool { //nolint:gocritic // hugeParam: Rules.SetupTokenRequired's signature
	if !s.deps.ServerParsed() {
		return true
	}
	return account.SetupLocality{Local: s.local, Hosts: s.deps.AllowedHosts()}.TokenRequired(ctx, c)
}

// local is the one locality rule setup and the password gate read.
func (s *Service) local(c access.ClientAddr) bool {
	return clientaddr.Local(c, s.deps.Forwarder)
}

// methodUsable answers from table 2B's record read through tx, so a
// removal's last-method guard judges on the committed record inside its own
// Update. A read error or a foreign tx counts the method unusable; no record
// leaves v7's own arms to decide. No passkey counts while the record's RP ID
// differs from rpID(), the published one, since v7 counts each passkey under
// the published RP ID; the OIDC arm needs no such check, since a user holds
// one link and v7's arm compares it with the published issuer.
func methodUsable(rpID func() string) func(context.Context, account.Tx, *access.User, access.Method) bool {
	return func(ctx context.Context, tx account.Tx, u *access.User, m access.Method) bool {
		st, ok, err := statedb.MethodStateOf(ctx, tx)
		if err != nil || !ok {
			return err == nil
		}
		switch m {
		case access.MethodPassword:
			return st.PasswordLogin
		case access.MethodOIDC:
			return st.OIDCIssuer != "" && u.OIDC.Issuer == st.OIDCIssuer
		case access.MethodPasskey:
			return st.RPID != "" && st.RPID == rpID()
		}
		return false
	}
}
