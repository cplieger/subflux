package settings

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// FuzzLoad_neverAllowAllWhenListed: for any file bytes, an inactive Hosts()
// implies the bytes hold no document or one mapping whose
// server.allowed_hosts is absent, null or an empty sequence, read here by
// yaml.v3 alone; Load never panics.
func FuzzLoad_neverAllowAllWhenListed(f *testing.F) {
	for _, seed := range []string{
		"", "# c\n", "server: {}\n", "server: {allowed_hosts: []}\n", "server: {allowed_hosts: ~}\n",
		"server: {allowed_hosts: [nas.lan], log_format: yaml}\n", "server: {allowed_hosts: [\" \"]}\n",
		"server: {allowed_hosts: nas.lan}\n", "server: {allowed_hosts: [~]}\n", "server: [x]\n",
		"server: {allowed_hosts: [nas.lan]}\n---\n", "a: &s {allowed_hosts: [x]}\nserver: *s\n",
		"<<: {server: {allowed_hosts: [x]}}\n", "a: &k server\n*k: {allowed_hosts: [x]}\n",
		"server: {allowed_hosts: []}\nserver: {allowed_hosts: [x]}\n", "server: !!map {allowed_hosts: !!seq []}\n",
		"server: {allowed_hosts: [nas.lan, '${SUBFLUX_UNSET}']}\n", "x: &a 1\nserver: {allowed_hosts: [nas.lan]}\n",
	} {
		f.Add([]byte(seed))
	}
	dir := f.TempDir()
	path := filepath.Join(dir, "config.yaml")
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		srv, _ := Load(path, Registries{}).Server()
		if !srv.Hosts().Active() && !unlisted(data) {
			t.Fatalf("Hosts() allows every Host for %q", data)
		}
	})
}

// unlisted reports whether data holds no document, or one mapping whose
// server key is absent or null, or whose allowed_hosts is absent, null or an
// empty sequence; a key it cannot name, an alias or a repeat counts as listed.
func unlisted(data []byte) bool {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return errors.Is(err, io.EOF)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return false
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false
	}
	server, ok := only(doc.Content[0], "server", true)
	if !ok {
		return false
	}
	if server == nil || isNullNode(server) {
		return true
	}
	if server.Kind != yaml.MappingNode {
		return false
	}
	hosts, ok := only(server, "allowed_hosts", false)
	if !ok {
		return false
	}
	return hosts == nil || isNullNode(hosts) || (hosts.Kind == yaml.SequenceNode && len(hosts.Content) == 0)
}

// only answers the value of key in m, nil when absent; false when key is set
// twice or a key's text cannot be read (an alias key resolves when
// resolveAlias holds).
func only(m *yaml.Node, key string, resolveAlias bool) (*yaml.Node, bool) {
	var found *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if resolveAlias && k.Kind == yaml.AliasNode && k.Alias != nil && k.Alias.Kind == yaml.ScalarNode {
			k = k.Alias
		}
		if k.Kind != yaml.ScalarNode || k.ShortTag() == "!!merge" {
			return nil, false
		}
		if k.Value != key {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = m.Content[i+1]
	}
	return found, true
}

func isNullNode(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" }
