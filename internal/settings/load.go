package settings

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"

	"github.com/cplieger/go/filesafe"
	"github.com/cplieger/go/yamlenv"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

// maxFileBytes bounds the file Load reads and Save writes.
const maxFileBytes = 1 << 20

// Load reads the file at path, an absolute path: absent is unconfigured, and
// a file that does not parse is Failed. It never writes.
func Load(path string, r Registries) Snapshot {
	data, err := filesafe.ReadBounded(context.Background(), path, maxFileBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return parse(nil, r, fileState{known: true})
	case errors.Is(err, filesafe.ErrFileTooLarge):
		return rootFailure("config.yaml is larger than 1 MiB", fileState{})
	case err != nil:
		return rootFailure("config.yaml cannot be read: "+err.Error(), fileState{})
	}
	return fromBytes(data, r)
}

// fromBytes is the snapshot of a file holding data.
func fromBytes(data []byte, r Registries) Snapshot {
	f := fileState{data: data, present: true, known: true}
	root, msg := decodeDocument(data)
	if msg != "" {
		return rootFailure(msg, f)
	}
	return parse(root, r, f)
}

// decodeDocument parses data as one YAML mapping; a file holding no document
// is an empty one. A non-empty message is a root-level failure.
func decodeDocument(data []byte) (root *yaml.Node, msg string) {
	if yamlenv.CheckSingleDocument(data) != nil {
		return nil, "config.yaml must hold one YAML document; remove the '---' separator"
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, "config.yaml is not valid YAML: " + yamlenv.SanitizeDecodeError(err).Error()
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}, ""
	}
	if doc.Content[0].Kind == yaml.MappingNode {
		return doc.Content[0], ""
	}
	return nil, "config.yaml must be a mapping of section names to sections"
}

// rootFailure is the Failed snapshot of a file with no readable mapping.
func rootFailure(msg string, f fileState) Snapshot {
	return Snapshot{&snapshot{
		mode:     Failed,
		failure:  []formschema.FieldError{{Message: msg}},
		server:   denyAllServer(),
		file:     f,
		revision: rand.Text(),
	}}
}

// parse builds the snapshot of root, the file's mapping, or of no file when
// root is nil, every section then at its owner's defaults. root is left as it
// was: the shape walk runs first, then expansion and every section's parse on
// a deep copy. A section whose subtree holds a shape or expansion error is
// failed without its parse running, so no owner walks an alias; a top-level
// key with no text (a merge key or a collection key) fails every section,
// the server one denying every Host. f is the file root was read from.
func parse(root *yaml.Node, r Registries, f fileState) Snapshot {
	mode, stored := ModeActive, root
	if root == nil {
		mode, root = Unconfigured, &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	}
	sh := checkShapes(root)
	errs, tainted := sh.errs, sh.tainted
	if sh.opaque {
		for _, s := range sections {
			tainted[s.key] = true
		}
	}
	doc := deepCopy(root)
	var names []string
	values := map[string][]*yaml.Node{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		name, ok := sectionName(doc.Content[i])
		if !ok {
			continue
		}
		v := doc.Content[i+1]
		if xerrs := expand(v, name); len(xerrs) > 0 {
			errs = append(errs, xerrs...)
			tainted[name] = true
		}
		if values[name] == nil {
			names = append(names, name)
		}
		values[name] = append(values[name], v)
	}
	active, failed, serrs := parseSections(r, names, values, tainted)
	errs = append(errs, serrs...)
	s := &snapshot{
		mode: mode, active: active, server: active.Server, serverOK: true, auth: active.Auth, authOK: true,
		fileAuth: active.Auth, file: f, revision: rand.Text(),
	}
	if stored != nil {
		s.doc, s.view = stored, view(stored, doc)
	}
	// With no file there is nothing to refuse: unconfigured runs each section
	// at its owner's defaults even where the owner refuses an absent section,
	// as languages does.
	if len(errs) == 0 || mode == Unconfigured {
		return Snapshot{s}
	}
	s.fail(errs, failed, sh.opaque, values["server"])
	return Snapshot{s}
}

// fail makes s Failed with errs, keeping each of the server and auth
// sections that parsed: a failed server section is its fail-closed value, or
// denies every Host when the file is opaque, and a failed auth section is the
// defaults.
func (s *snapshot) fail(errs []formschema.FieldError, failed map[string]bool, opaque bool, server []*yaml.Node) {
	s.mode, s.failure, s.serverOK, s.authOK = Failed, errs, !failed["server"], !failed[accounts.SectionKey]
	if !s.authOK {
		s.auth = accounts.Config{}
	}
	switch {
	case opaque:
		s.server = denyAllServer()
	case failed["server"]:
		s.server = failClosedServer(server)
	}
}
