package config

import (
	"fmt"
	"strings"

	"github.com/cplieger/webhttp"
)

// HostAllowlist returns the parsed exact-match Host allowlist built from
// allowed_hosts. It is inactive (a safe pass-through) when allowed_hosts is
// unset — the backward-compatible default — and active as soon as any
// non-blank entry is configured. Not part of the ConfigProvider interface;
// consumed directly by the server's host gate (like TrustedProxyNets).
func (c *Config) HostAllowlist() *webhttp.HostPolicy {
	return c.cachedHostPolicy
}

// parseAllowedHosts parses the allowed_hosts entries via webhttp.ParseHostList
// into the exact-match Host allowlist that closes the DNS-rebinding hole the
// CSRF check alone leaves open: a rebinding attack makes Origin and Host
// AGREE, so http.CrossOriginProtection admits it; only an exact-Host check
// breaks that chain (CWE-346). Each entry is a bare hostname or IP subflux
// answers for ("subflux.example.com", "192.168.1.5") — no scheme, path, or
// port. The loopback carve-out is enabled so the container's own healthcheck
// and in-container clients keep working under any allowlist, and the 403
// names allowed_hosts so a locked-out operator knows which knob to fix.
//
// Strict, like parseTrustedProxies: any entry CanonicalHost rejects yields a
// field-tagged validation error naming the offenders. The policy built from
// the valid subset is still returned alongside the error — per ParseHostList's
// fail-closed contract an active-but-diminished allowlist must keep gating
// (deny-all when nothing valid remains) rather than silently deactivate.
func parseAllowedHosts(entries []string) (*webhttp.HostPolicy, error) {
	policy, invalid := webhttp.ParseHostList(entries,
		webhttp.WithLoopbackExempt(),
		webhttp.WithHostAllowlistError("host_not_allowed",
			"host not allowed; add it to allowed_hosts in the server settings"))
	if len(invalid) > 0 {
		return policy, configFieldErr("allowed_hosts",
			fmt.Sprintf("invalid allowed_hosts entries [%s]: each must be a bare hostname "+
				"such as subflux.example.com or an IP such as 192.168.1.5 "+
				"(no scheme, path, or port)", strings.Join(invalid, ", ")))
	}
	return policy, nil
}
