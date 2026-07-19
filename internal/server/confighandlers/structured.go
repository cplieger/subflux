package confighandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
	yaml "go.yaml.in/yaml/v3"
)

// Structured config save (P16). The browser submits typed JSON keyed by
// top-level config section; the SERVER merges omitted secrets using the
// schema's Secret metadata and serializes the canonical YAML itself. The
// browser-side YAML codec is gone, and "which fields are secret" is
// structural (schema-declared) instead of a name-pattern list. The raw-YAML
// GET/PUT survive unchanged for hand editing and the API.
//
// JSON is a subset of YAML, so each section's raw JSON parses directly into
// a yaml.Node tree; assembling the document in schema section order and
// letting yaml.Marshal render it IS the canonical serializer — no duplicate
// struct tree, no hand-rolled emitter. Like the previous UI save path, a
// structured save regenerates the file from form values: unknown hand-added
// keys and comments are not preserved (unchanged behavior, documented in
// the steering doc).

// StructuredConfig is the wire shape both structured endpoints share.
type StructuredConfig struct {
	Sections map[string]json.RawMessage `json:"sections"`
	// SecretsPresent lists the dotted schema paths of secrets that hold a
	// non-empty value in the config file (e.g. "sonarr.api_key",
	// "providers.opensubtitles.settings.password"). GET-only metadata: the
	// values themselves stay redacted-empty, but the first-boot wizard needs
	// presence to distinguish "key saved" from "key missing" for its
	// satisfied/ping decisions. Ignored on save.
	SecretsPresent []string `json:"secrets_present,omitempty"`
}

// fullSchema resolves the complete schema (app sections + provider registry).
// A nil registry (possible in tests and stripped-down assemblies) yields the
// app sections with no provider entries rather than a panic.
func (h *Handler) fullSchema() []api.SchemaSection {
	var provs []api.ProviderSchema
	if h.registry != nil {
		provs = api.BuildProviderSchemas(h.registry, string(api.ProviderNameMock))
	}
	return h.schemaFunc(provs)
}

// HandleGetConfigStructured returns the current config file as structured
// JSON, sections keyed by their YAML name, with every schema-declared secret
// redacted to the empty string (the save-side merge treats empty as "keep
// the existing value", so a round-trip through an untouched form preserves
// secrets).
func (h *Handler) HandleGetConfigStructured(w http.ResponseWriter, r *http.Request) {
	data, err := atomicfile.ReadBounded(r.Context(), h.configPath(), maxBodySize)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "read config", "path", h.configPath())
		return
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "parse config")
		return
	}
	doc := documentMapping(&root)
	if doc == nil {
		// Empty or scalar file: serve an empty section set; the form renders
		// schema defaults exactly as it would for a blank config.
		api.WriteJSON(w, StructuredConfig{Sections: map[string]json.RawMessage{}})
		return
	}

	// Record which schema-declared secrets carry a value BEFORE redacting
	// them: presence is the only secret metadata the client may see.
	var present []string
	for _, path := range secretPaths(h.fullSchema()) {
		if node := resolvePath(doc, path); node != nil && node.Kind == yaml.ScalarNode && node.Value != "" {
			present = append(present, strings.Join(path, "."))
		}
		redactNodePath(doc, path)
	}

	sections := make(map[string]json.RawMessage)
	for i := 0; i+1 < len(doc.Content); i += 2 {
		keyNode, valNode := doc.Content[i], doc.Content[i+1]
		var v any
		if err := valNode.Decode(&v); err != nil {
			api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "decode section", "section", keyNode.Value)
			return
		}
		raw, err := json.Marshal(v)
		if err != nil {
			api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "encode section", "section", keyNode.Value)
			return
		}
		sections[keyNode.Value] = raw
	}
	api.WriteJSON(w, StructuredConfig{Sections: sections, SecretsPresent: present})
}

// HandleSaveConfigStructured validates, secret-merges, canonicalizes, and
// applies a structured JSON config. After canonicalization it runs the exact
// raw-save pipeline (parse -> validate -> arr ping -> hot reload -> atomic
// write), so both save paths share every safety property and error code.
func (h *Handler) HandleSaveConfigStructured(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))
	if err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "failed to read body")
		return
	}
	if int64(len(body)) > maxBodySize {
		slog.Warn("structured config request body too large", "size", len(body))
		api.PayloadTooLargeC(w, r, api.CodeConfigTooLarge, "request body too large")
		return
	}

	var sc StructuredConfig
	if err = json.Unmarshal(body, &sc); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(sc.Sections) == 0 {
		api.BadRequestC(w, r, api.CodeConfigInvalid, "no config sections provided")
		return
	}

	// One save transaction at a time, from the baseline read inside
	// canonicalYAML through persistence (see saveMu).
	h.saveMu.Lock()
	defer h.saveMu.Unlock()

	data, err := h.canonicalYAML(r.Context(), &sc)
	if err != nil {
		if errors.Is(err, errBaselineUnavailable) {
			// Not the client's fault: the payload relies on keep-semantics
			// secrets and the server could not read its own existing config.
			// Fail closed — no save, no activation; details go to the log.
			api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "secret merge")
			return
		}
		api.BadRequestC(w, r, api.CodeConfigInvalid, "invalid configuration: "+err.Error())
		return
	}
	slog.Debug("structured config canonicalized", "bytes", len(data), "sections", len(sc.Sections))

	h.applyConfig(w, r, data)
}

// canonicalYAML converts the structured sections into the canonical YAML
// document: schema section order first (then any extra sections in sorted
// order for determinism), with schema-declared secrets merged from the
// existing config file when the incoming value is empty.
func (h *Handler) canonicalYAML(ctx context.Context, sc *StructuredConfig) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	if err := h.assembleSections(doc, sc); err != nil {
		return nil, err
	}

	// Merge schema-declared secrets from the existing file: an empty
	// incoming value means "keep what I have" (the GET side redacts to
	// empty, so an untouched form round-trips every secret). Fails closed
	// when the payload needs the baseline and the baseline is unreadable.
	if err := h.mergeExistingSecrets(ctx, doc); err != nil {
		return nil, err
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}
	return out, nil
}

// assembleSections appends the submitted sections to the document mapping in
// canonical order: schema section order first, then any extra sections in
// sorted order for determinism.
func (h *Handler) assembleSections(doc *yaml.Node, sc *StructuredConfig) error {
	seen := make(map[string]bool)
	sections := h.fullSchema()
	for i := range sections {
		key := sections[i].Key
		raw, ok := sc.Sections[key]
		if !ok {
			continue
		}
		seen[key] = true
		if err := appendSection(doc, key, raw); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(sc.Sections) {
		if seen[name] {
			continue
		}
		if err := appendSection(doc, name, sc.Sections[name]); err != nil {
			return err
		}
	}
	return nil
}

// appendSection parses one raw JSON section into a yaml.Node tree (JSON is
// valid YAML, so it parses directly) and appends it to the document mapping.
func appendSection(doc *yaml.Node, name string, raw json.RawMessage) error {
	var val yaml.Node
	if err := yaml.Unmarshal(raw, &val); err != nil {
		return fmt.Errorf("section %q: %w", name, err)
	}
	content := &val
	if val.Kind == yaml.DocumentNode && len(val.Content) == 1 {
		content = val.Content[0]
	}
	doc.Content = append(doc.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, content)
	return nil
}

// errBaselineUnavailable marks a canonicalYAML failure caused by the
// existing config file (the secret-merge baseline) being unreadable or
// unparseable while the incoming payload relies on keep semantics. Distinct
// from a payload error: the submitted config may be perfectly valid, so the
// save handler maps this to a 500 instead of a 400.
var errBaselineUnavailable = errors.New("secret-merge baseline unavailable")

// mergeExistingSecrets fills the incoming document's empty or omitted
// schema-declared secrets from the existing config file. An empty incoming
// secret means "keep what I have", which makes the baseline's readability a
// correctness input: if the payload relies on keep semantics and the
// existing file cannot be read (any error other than not-exist) or
// understood (YAML parse failure, non-mapping root), silently skipping the
// merge would convert every "keep" into deletion. Those cases fail closed
// via errBaselineUnavailable — no save, no activation.
//
// Two cases deliberately proceed without a merge: a missing file and an
// empty/null document are TRUE empty baselines (provably nothing to keep;
// the unconfigured-mode first save must not fail closed on them), and a
// payload with no keep-semantics secrets never needs the baseline at all —
// which also lets a complete payload overwrite, and thereby repair, a
// corrupted config file.
func (h *Handler) mergeExistingSecrets(ctx context.Context, doc *yaml.Node) error {
	paths := secretPaths(h.fullSchema())
	if !hasKeepSecrets(doc, paths) {
		return nil
	}
	existing, err := atomicfile.ReadBounded(ctx, h.configPath(), maxBodySize)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%w: read existing config: %w", errBaselineUnavailable, err)
	}
	var existingRoot yaml.Node
	if err := yaml.Unmarshal(existing, &existingRoot); err != nil {
		return fmt.Errorf("%w: parse existing config: %w", errBaselineUnavailable, err)
	}
	existingDoc := documentMapping(&existingRoot)
	if existingDoc == nil {
		if emptyYAMLDocument(&existingRoot) {
			return nil
		}
		return fmt.Errorf("%w: existing config is not a YAML mapping", errBaselineUnavailable)
	}
	for _, path := range paths {
		mergeSecretPath(doc, existingDoc, path)
	}
	return nil
}

// hasKeepSecrets reports whether the incoming document carries at least one
// schema-declared secret with keep semantics: an empty scalar leaf, or a
// leaf omitted under a present parent mapping (both are how the redacting
// GET round-trips an untouched secret; both are what mergeSecretPath fills).
// Only these depend on the baseline; a non-empty secret and a deleted
// section/provider (absent parent) never do.
func hasKeepSecrets(doc *yaml.Node, paths []secretPath) bool {
	for _, path := range paths {
		parent := doc
		for _, key := range path[:len(path)-1] {
			parent = childValue(parent, key)
		}
		if parent == nil || parent.Kind != yaml.MappingNode {
			continue
		}
		leaf := childValue(parent, path[len(path)-1])
		if leaf == nil || (leaf.Kind == yaml.ScalarNode && leaf.Value == "") {
			return true
		}
	}
	return false
}

// emptyYAMLDocument reports whether a successfully parsed YAML file has no
// content at all: zero bytes or comments-only input (the parser yields a
// zero node), a bare document with no content, or an explicit null
// document. Such a baseline provably holds no secrets, so keep-semantics
// saves may proceed against it exactly as against a missing file.
func emptyYAMLDocument(root *yaml.Node) bool {
	switch {
	case root.Kind == 0:
		return true
	case root.Kind != yaml.DocumentNode:
		return false
	case len(root.Content) == 0:
		return true
	case len(root.Content) == 1:
		c := root.Content[0]
		return c.Kind == yaml.ScalarNode && c.Tag == "!!null"
	default:
		return false
	}
}

// applyConfig is the shared save tail: parse+validate, arr connectivity,
// hot reload (apply BEFORE persist), atomic write, success envelope. Both
// the raw-YAML and structured save handlers end here, so the two paths can
// never drift in safety behavior or error codes.
//
// Callers must hold saveMu: the save entry points acquire it before any
// existing-file read or canonicalization work so the whole transaction —
// merge, compare, ping, activate, persist — is serialized (Go mutexes are
// not reentrant, so the lock cannot live here).
func (h *Handler) applyConfig(w http.ResponseWriter, r *http.Request, data []byte) {
	newCfg, err := h.loadConfig(data)
	if err != nil {
		api.BadRequestC(w, r, api.CodeConfigInvalid, "invalid configuration: "+err.Error())
		return
	}

	oldCfg := h.state().Cfg
	if pingErr := h.pingArrIfChanged(r.Context(), "sonarr", newCfg.SonarrConfig(), oldCfg); pingErr != nil {
		api.BadRequestC(w, r, api.CodeConfigUnreachableArr, "sonarr unreachable: "+pingErr.Error())
		return
	}
	if pingErr := h.pingArrIfChanged(r.Context(), "radarr", newCfg.RadarrConfig(), oldCfg); pingErr != nil {
		api.BadRequestC(w, r, api.CodeConfigUnreachableArr, "radarr unreachable: "+pingErr.Error())
		return
	}

	// Apply BEFORE persisting: a config that parses but fails wiring (bad
	// provider key, invalid arr URL) must not land on disk, or the running
	// state silently diverges from the file and the next restart drops into
	// unconfigured mode unexpectedly. On a reload failure nothing changed —
	// neither the live state nor the file.
	if err := h.hotReload(r.Context(), newCfg); err != nil {
		slog.Error("hot reload failed, config not saved", "error", err)
		h.alerts.RecordPersistent("config",
			"Config rejected (hot reload failed): "+err.Error())
		api.InternalErrorC(w, r, fmt.Errorf("reload failed: %w", err), api.CodeConfigReloadFailed)
		return
	}

	// Atomic write: temp file + rename prevents corruption on crash. A write
	// failure here leaves the NEW config applied in memory but the OLD file
	// on disk (a restart reverts) — rarer and safer than the reverse, and
	// reported loudly so the operator can free disk space and re-save.
	if err := atomicWriteConfig(r.Context(), h.configPath(), data); err != nil {
		slog.Error("config applied but not persisted", "error", err)
		h.alerts.RecordPersistent("config",
			"Config applied but NOT saved to disk (a restart will revert it): "+err.Error())
		api.InternalErrorC(w, r, fmt.Errorf("config applied but not persisted: %w", err),
			api.CodeInternalError, "stage", "write config")
		return
	}

	slog.Info("config saved and hot-reloaded")
	api.WriteJSON(w, api.StatusResponse{Status: "saved and applied"})
}

// --- schema-driven secret paths + yaml.Node plumbing ---

// secretPath addresses one schema-declared secret inside the YAML document,
// e.g. ["sonarr","api_key"] or ["providers","opensubtitles","settings","password"].
type secretPath []string

// secretPaths derives every secret's document path from the schema: the
// merge and redaction are structural consequences of `Secret: true`, so a
// new secret field is covered the moment it is declared (no name list to
// forget; see TestSecretPaths_cover_every_schema_secret).
func secretPaths(schema []api.SchemaSection) []secretPath {
	var paths []secretPath
	var fieldPaths func(prefix []string, fields []api.SchemaField)
	fieldPaths = func(prefix []string, fields []api.SchemaField) {
		for i := range fields {
			f := &fields[i]
			p := append(append([]string{}, prefix...), f.Key)
			if f.Secret {
				paths = append(paths, p)
			}
			if len(f.Fields) > 0 {
				fieldPaths(p, f.Fields)
			}
		}
	}
	for i := range schema {
		section := &schema[i]
		if section.Type == "providers" {
			for j := range section.Providers {
				prov := &section.Providers[j]
				fieldPaths([]string{section.Key, prov.Name, "settings"}, prov.Settings)
			}
			continue
		}
		fieldPaths([]string{section.Key}, section.Fields)
	}
	return paths
}

// documentMapping unwraps a parsed YAML document to its top-level mapping
// node, or nil when the document is empty or not a mapping.
func documentMapping(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

// childValue returns the value node for key within a mapping node.
func childValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// resolvePath walks a secret path to its scalar value node.
func resolvePath(mapping *yaml.Node, path secretPath) *yaml.Node {
	node := mapping
	for _, key := range path {
		node = childValue(node, key)
		if node == nil {
			return nil
		}
	}
	return node
}

// redactNodePath blanks the scalar at path (GET-side redaction).
func redactNodePath(doc *yaml.Node, path secretPath) {
	if node := resolvePath(doc, path); node != nil && node.Kind == yaml.ScalarNode && node.Value != "" {
		node.SetString("")
	}
}

// mergeSecretPath copies the existing file's secret into the incoming
// document when the incoming value is empty or absent-but-addressable. An
// incoming path whose parent mapping does not exist is left alone: the user
// removed that section or provider, and resurrecting its secret would
// resurrect config they deleted.
func mergeSecretPath(incoming, existing *yaml.Node, path secretPath) {
	existingVal := resolvePath(existing, path)
	if existingVal == nil || existingVal.Kind != yaml.ScalarNode || existingVal.Value == "" {
		return
	}

	parent := incoming
	for _, key := range path[:len(path)-1] {
		parent = childValue(parent, key)
		if parent == nil {
			return
		}
	}
	leaf := path[len(path)-1]
	if node := childValue(parent, leaf); node != nil {
		if node.Kind == yaml.ScalarNode && node.Value == "" {
			node.SetString(existingVal.Value)
		}
		return
	}
	if parent.Kind != yaml.MappingNode {
		return
	}
	// Field omitted entirely (e.g. the form never renders a value for an
	// untouched secret): add it back from the existing file.
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: leaf}
	valNode := &yaml.Node{}
	valNode.SetString(existingVal.Value)
	parent.Content = append(parent.Content, keyNode, valNode)
}

// sortedKeys returns the map's keys in sorted order for deterministic output.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
