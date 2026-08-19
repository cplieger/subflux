package config

import (
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/cplieger/webhttp/v2"
)

// TrustedProxyNets returns the parsed trusted reverse-proxy CIDR set used for
// spoof-safe client-IP resolution. It is empty when trusted_proxies is unset
// (the default), in which case the client IP resolves to the unspoofable
// socket peer and X-Forwarded-For is ignored. Not part of the ConfigProvider
// interface; consumed directly by the composition root (like AuthDisabled).
//
// The returned set is a deep copy. This one is worth the allocation: it is the
// set that decides whether X-Forwarded-For is honoured, so a caller holding a
// live *net.IPNet could widen the trusted range — and net.IPNet's own fields
// are byte slices, so copying only the pointers or only the slice would still
// share the address and mask a caller can overwrite in place.
func (c *Config) TrustedProxyNets() []*net.IPNet {
	if c.cachedTrustedProxies == nil {
		return nil
	}
	out := make([]*net.IPNet, len(c.cachedTrustedProxies))
	for i, n := range c.cachedTrustedProxies {
		if n == nil {
			continue
		}
		out[i] = &net.IPNet{
			IP:   slices.Clone(n.IP),
			Mask: slices.Clone(n.Mask),
		}
	}
	return out
}

// parseTrustedProxies parses the trusted_proxies entries via
// webhttp.ParseCIDRs, the same helper webhttp.ClientIP consumes. Each entry is
// CIDR notation (e.g. "10.0.0.0/8") or a bare IP treated as a single host
// ("192.168.1.5" -> /32, "::1" -> /128); surrounding whitespace is trimmed and
// blank entries are skipped. Strict: any entry that is neither a valid CIDR nor
// a bare IP yields a field-tagged validation error naming the offenders.
func parseTrustedProxies(cidrs []string) ([]*net.IPNet, error) {
	nets, invalid := webhttp.ParseCIDRs(cidrs)
	if len(invalid) > 0 {
		return nil, configFieldErr("trusted_proxies",
			fmt.Sprintf("invalid trusted_proxies entries [%s]: each must be CIDR notation "+
				"such as 10.0.0.0/8 or a bare IP such as 192.168.1.5", strings.Join(invalid, ", ")))
	}
	return nets, nil
}
