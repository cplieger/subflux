package accounts

import (
	"net/http"
	"slices"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesshttp"
)

// The paths the access routes are served under, and where the OIDC callback
// sends a browser on a non-success outcome.
const (
	routePrefix     = "/api/auth"
	routeOIDCPrefix = "/api/auth/oidc"
	signInPath      = "/login"
)

const routeLoginPassword = "authLoginPassword" //nolint:gosec // G101: a route name, not a credential

// unmounted names the access routes subflux does not serve (v7 §13.3):
// passkey setup and recovery, a link started from a signed-in session, and
// password removal.
var unmounted = []string{
	"authSetupPasskeyBegin",
	"authSetupPasskeyFinish",
	"authOIDCStartLink",
	"authPasswordRemove",
	"authRecoveryPasskeyBegin",
	"authRecoveryPasskeyFinish",
}

func isUnmounted(name string) bool { return slices.Contains(unmounted, name) }

// descriptorConfig is the route configuration Routes and Descriptors share.
func descriptorConfig() accesshttp.DescriptorConfig {
	return accesshttp.DescriptorConfig{
		Prefix: routePrefix, OIDCPrefix: routeOIDCPrefix,
		AdminScope: scopeAdmin, ManageUsersScope: scopeManageUsers, CreateKeysScope: scopeCreateKeys,
	}
}

// Descriptors is the descriptors Routes mounts, built from accesshttp's
// static data with no store, so a generator or the CLI reads them without an
// account service.
func Descriptors() []accesshttp.Descriptor {
	return slices.DeleteFunc(accesshttp.Descriptors(descriptorConfig()), func(d accesshttp.Descriptor) bool { return isUnmounted(d.Name) })
}

// Routes is every access route subflux serves, each refusal written through
// errs and each audit event queued for Run. Password sign-in is refused to a
// client that is not local while the auth section failed to parse.
func (s *Service) Routes(errs accesshttp.ErrorWriter) ([]accesshttp.Route, error) {
	dc := descriptorConfig()
	h, err := accesshttp.New(accesshttp.Config{
		Account: s.account, Errors: errs, Audit: s.audit.enqueue, Logger: s.deps.Logger,
		Prefix: dc.Prefix, OIDCPrefix: dc.OIDCPrefix, SignInPath: signInPath,
	})
	if err != nil {
		return nil, err
	}
	routes := slices.DeleteFunc(h.Routes(), func(r accesshttp.Route) bool { return isUnmounted(r.Name) })
	for i := range routes {
		if routes[i].Name == routeLoginPassword {
			routes[i].Handler = s.passwordGate(routes[i].Handler, errs)
		}
	}
	return routes, nil
}

// passwordGate is the password sign-in route behind the unparsed-section
// rule: password_login is unknown then, so only a local client may use it.
func (s *Service) passwordGate(next http.Handler, errs accesshttp.ErrorWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.deps.Config(); ok {
			next.ServeHTTP(w, r)
			return
		}
		addr := s.clientAddr(r)
		if s.local(addr) {
			next.ServeHTTP(w, r)
			return
		}
		e := accesshttp.AuditEvent{Kind: "login.failure", Method: access.MethodPassword, Reason: "password_login_disabled"}
		if ip := addr.IP(); ip.IsValid() {
			e.IP = ip.String()
		}
		s.audit.enqueue(r, e)
		w.Header().Set("Cache-Control", "no-store")
		errs(w, r, accesshttp.Error{
			Status: http.StatusForbidden, Code: accesshttp.CodePasswordLoginDisabled,
			Detail: "Until the auth settings are repaired, password sign-in works only from a device on this server's own network that connects directly, not through a proxy.",
		})
	})
}
