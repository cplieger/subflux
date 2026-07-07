package config

import (
	"fmt"
	"net"
	"strings"
)

// TrustedProxyNets returns the parsed trusted reverse-proxy CIDR set used for
// spoof-safe client-IP resolution. It is empty when trusted_proxies is unset
// (the default), in which case the client IP resolves to the unspoofable
// socket peer and X-Forwarded-For is ignored. Not part of the ConfigProvider
// interface; consumed directly by the composition root (like AuthDisabled).
func (c *Config) TrustedProxyNets() []*net.IPNet {
	return c.cachedTrustedProxies
}

// parseTrustedProxies parses each trusted_proxies entry with net.ParseCIDR.
// Every entry must be CIDR notation; a single proxy is written as a /32
// (IPv4) or /128 (IPv6), e.g. "192.168.1.5/32". Blank entries are skipped.
// An invalid entry yields a field-tagged validation error naming the offender.
func parseTrustedProxies(cidrs []string) ([]*net.IPNet, error) {
	if len(cidrs) == 0 {
		return nil, nil
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, entry := range cidrs {
		s := strings.TrimSpace(entry)
		if s == "" {
			continue
		}
		_, network, err := net.ParseCIDR(s)
		if err != nil {
			return nil, configFieldErr("trusted_proxies",
				fmt.Sprintf("invalid trusted_proxies entry %q: must be CIDR notation "+
					"such as 10.0.0.0/8 or 192.168.1.5/32", s))
		}
		nets = append(nets, network)
	}
	if len(nets) == 0 {
		return nil, nil
	}
	return nets, nil
}
