package settings

import (
	"log/slog"
	"net/netip"
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/webhttp/v3"
	"go.yaml.in/yaml/v3"
)

const (
	formatJSON = "json"
	formatText = "text"
	levelInfo  = "info"
)

const (
	keyTrustedProxies = "trusted_proxies"
	keyAllowedHosts   = "allowed_hosts"
	keyLogLevel       = "log_level"
	keyLogFormat      = "log_format"
)

// rootEntry is the host-list entry a root-level failure passes to
// webhttp.ParseHostList: non-blank so the policy engages, and refused by
// CanonicalHost so it admits no Host.
const rootEntry = "(root)"

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug, levelInfo: slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
}

// serverSchema is the server section's form.
var serverSchema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: keyTrustedProxies, Label: "Trusted proxies", Kind: formschema.StringList,
		Help: "Addresses or CIDR ranges of reverse proxies whose forwarded client address is believed.",
	},
	{
		Key: keyAllowedHosts, Label: "Allowed hosts", Kind: formschema.StringList,
		Help: "Host names the web interface answers to; empty answers every host.",
	},
	{
		Key: keyLogLevel, Label: "Log level", Kind: formschema.Enum,
		Enum: []string{"debug", levelInfo, "warn", "error"}, Default: levelInfo,
	},
	{
		Key: keyLogFormat, Label: "Log format", Kind: formschema.Enum,
		Enum: []string{formatJSON, formatText}, Default: formatJSON, Help: "Takes effect after a restart.",
	},
}}

// Server is the server section; its zero value trusts no proxy, allows every
// host and logs at INFO in JSON.
type Server struct{ p *server }

type server struct {
	hosts   *webhttp.HostPolicy
	format  string
	proxies []netip.Prefix
	allowed []string
	level   slog.Level
}

// TrustedProxies answers a copy of the proxies whose forwarded address is
// believed.
func (s Server) TrustedProxies() []netip.Prefix {
	if s.p == nil {
		return nil
	}
	return slices.Clone(s.p.proxies)
}

// Hosts is webhttp.ParseHostList over allowed_hosts with
// WithLoopbackExempt(true), so a loopback peer naming a loopback Host is
// admitted: inactive only when no file exists or it is one mapping whose list
// is absent, null or empty. A list with any element but no valid entry, or a
// root-level failure, denies every Host, a loopback peer's included.
func (s Server) Hosts() *webhttp.HostPolicy {
	if s.p == nil {
		return nil
	}
	return s.p.hosts
}

// AllowedHosts is the canonical hosts of the allowed_hosts entries
// ParseHostList accepted, once each in file order, nil for none.
func (s Server) AllowedHosts() []string {
	if s.p == nil {
		return nil
	}
	return slices.Clone(s.p.allowed)
}

// LogLevel is the level the process logs at.
func (s Server) LogLevel() slog.Level {
	if s.p == nil {
		return slog.LevelInfo
	}
	return s.p.level
}

// LogFormat is json or text.
func (s Server) LogFormat() string {
	if s.p == nil {
		return formatJSON
	}
	return s.p.format
}

// parseServer reads the server section, nil when absent. Each list entry its
// parser refuses is a field error at its index, so a Server never runs a
// partial list.
func parseServer(n *yaml.Node) (Server, []formschema.FieldError) {
	vals, errs := serverSchema.Decode(n)
	s := &server{}
	proxies, _ := vals.StringList(keyTrustedProxies)
	for i, e := range proxies {
		p, ok := parseProxy(e)
		if !ok {
			errs = append(errs, formschema.FieldError{Path: index(keyTrustedProxies, i), Message: "must be an IP address or a CIDR range such as 10.0.0.0/8"})
			continue
		}
		s.proxies = append(s.proxies, p)
	}
	hosts, _ := vals.StringList(keyAllowedHosts)
	for i, e := range hosts {
		if webhttp.CanonicalHost(strings.TrimSpace(e)) == "" {
			errs = append(errs, formschema.FieldError{Path: index(keyAllowedHosts, i), Message: "must be a host name or address, with an optional port, such as nas.lan or nas.lan:8374"})
		}
	}
	level, _ := vals.String(keyLogLevel)
	s.level = logLevels[level]
	s.format, _ = vals.String(keyLogFormat)
	if len(errs) > 0 {
		slices.SortStableFunc(errs, func(a, b formschema.FieldError) int {
			return fieldOrder(a.Path) - fieldOrder(b.Path)
		})
		return Server{}, formschema.Prefix("server", errs)
	}
	s.hosts, s.allowed = hostPolicy(hosts, true)
	return Server{s}, nil
}

// fieldOrder is the schema position of the field a section-relative path
// names, so a section's errors read in form order.
func fieldOrder(path string) int {
	key, _, _ := strings.Cut(path, "[")
	return slices.IndexFunc(serverSchema.Fields, func(f formschema.Field) bool { return f.Key == key })
}

func parseProxy(e string) (netip.Prefix, bool) {
	e = strings.TrimSpace(e)
	if p, err := netip.ParsePrefix(e); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(e); err == nil && a.Zone() == "" {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}

// hostPolicy is ParseHostList over entries, and the canonical hosts it
// accepted, once each in file order: the keys its policy matches, a port
// dropped.
func hostPolicy(entries []string, loopbackExempt bool) (policy *webhttp.HostPolicy, allowed []string) {
	policy, _ = webhttp.ParseHostList(entries, webhttp.WithLoopbackExempt(loopbackExempt))
	for _, e := range entries {
		if h := webhttp.CanonicalHost(strings.TrimSpace(e)); h != "" && !slices.Contains(allowed, h) {
			allowed = append(allowed, h)
		}
	}
	return policy, allowed
}

// denyAllServer is a root-level failure's fail-closed value: default log
// fields, no trusted proxy, every Host refused.
func denyAllServer() Server {
	p, _ := webhttp.ParseHostList([]string{rootEntry})
	return Server{&server{hosts: p, level: slog.LevelInfo, format: formatJSON}}
}

// failClosedServer is a failed server section's value, read from its nodes as
// written (two when the key is repeated): no trusted proxy, each log field its
// own value when valid, else its default, and a host policy of the entries as
// written. Each refused element passes its path text, which CanonicalHost
// refuses, so a list with no valid entry denies every Host; a list it cannot
// read, an alias or a merge key denies every Host. It follows no alias. It
// reads a null by its YAML tag, not formschema.IsNull: a failed section can
// hold a written tag, and IsNull reads !!str ~ as absent, admitting any Host.
func failClosedServer(nodes []*yaml.Node) Server {
	if len(nodes) != 1 {
		return denyAllServer()
	}
	n := nodes[0]
	if n.Kind == yaml.ScalarNode && n.ShortTag() == tagNull {
		return Server{}
	}
	if n.Kind != yaml.MappingNode {
		return denyAllServer()
	}
	fields := map[string][]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if k.Kind != yaml.ScalarNode || k.ShortTag() == tagMerge {
			return denyAllServer()
		}
		fields[k.Value] = append(fields[k.Value], n.Content[i+1])
	}
	s := &server{level: slog.LevelInfo, format: formatJSON}
	if v, ok := enumText(fields[keyLogLevel]); ok {
		if l, ok := logLevels[v]; ok {
			s.level = l
		}
	}
	if v, ok := enumText(fields[keyLogFormat]); ok && (v == formatJSON || v == formatText) {
		s.format = v
	}
	entries, ok := writtenHosts(fields[keyAllowedHosts])
	if !ok {
		entries = []string{rootEntry}
	}
	s.hosts, s.allowed = hostPolicy(entries, false)
	return Server{s}
}

// enumText is a field's text when it is set once to a scalar.
func enumText(nodes []*yaml.Node) (string, bool) {
	if len(nodes) != 1 || nodes[0].Kind != yaml.ScalarNode {
		return "", false
	}
	return nodes[0].Value, true
}

// writtenHosts is allowed_hosts as written, each element that is not a
// non-blank scalar replaced by its path text; false when the list is repeated
// or not a list.
func writtenHosts(nodes []*yaml.Node) ([]string, bool) {
	switch {
	case len(nodes) == 0:
		return nil, true
	case len(nodes) > 1:
		return nil, false
	}
	n := nodes[0]
	if n.Kind == yaml.ScalarNode && n.ShortTag() == tagNull {
		return nil, true
	}
	if n.Kind != yaml.SequenceNode {
		return nil, false
	}
	entries := make([]string, 0, len(n.Content))
	for i, e := range n.Content {
		text := strings.TrimSpace(e.Value)
		if e.Kind != yaml.ScalarNode || e.ShortTag() == tagNull || text == "" {
			text = index("server."+keyAllowedHosts, i)
		}
		entries = append(entries, text)
	}
	return entries, true
}
