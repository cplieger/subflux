// Package settings loads, holds and saves subflux's one configuration file:
// each section parsed by its owner, the result a Snapshot that is
// Unconfigured, Failed or Active, never a configuration beside a flag.
package settings

import (
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"go.yaml.in/yaml/v3"
)

// Mode is which of the three states a Snapshot is in.
type Mode uint8

const (
	// Unconfigured is no file: every section runs at its owner's defaults.
	Unconfigured Mode = iota + 1
	// Failed is a file that does not parse; Failure holds why.
	Failed
	// ModeActive is a file that parsed; Active holds it.
	ModeActive
)

var modeNames = []string{"unconfigured", "failed", "active"}

var errUnknownMode = errors.New("settings: unknown mode")

// MarshalText answers the mode's snake_case name and refuses the zero Mode.
func (m Mode) MarshalText() ([]byte, error) {
	if m < Unconfigured || m > ModeActive {
		return nil, fmt.Errorf("%w: %d", errUnknownMode, m)
	}
	return []byte(modeNames[m-1]), nil
}

// UnmarshalText reads a snake_case mode name, refusing every other spelling.
func (m *Mode) UnmarshalText(b []byte) error {
	for v := Unconfigured; v <= ModeActive; v++ {
		if modeNames[v-1] == string(b) {
			*m = v
			return nil
		}
	}
	return fmt.Errorf("%w: %q", errUnknownMode, b)
}

// Registries lists the adapter kinds each hand-walked section accepts.
type Registries struct{}

// Active is a configuration that parsed, one field per section.
type Active struct {
	Languages language.Policy
	Server    Server
}

// Snapshot is one immutable reading of the file. Only Load and Service.Save
// build one, so a mode and a configuration never disagree. The zero Snapshot
// has no mode, and its Server denies every Host.
type Snapshot struct{ p *snapshot }

type snapshot struct {
	active Active
	// server is the section in force; serverOK is false only in Failed when
	// the section's own parse failed, and server is then its fail-closed value.
	server Server
	// doc is the file's mapping as written, before expansion, and view is
	// Document's node for it; both nil when there is no mapping.
	doc, view *yaml.Node
	revision  string
	failure   []formschema.FieldError
	file      fileState
	mode      Mode
	serverOK  bool
}

// Mode answers which state the snapshot is in.
func (s Snapshot) Mode() Mode {
	if s.p == nil {
		return 0
	}
	return s.p.mode
}

// Active answers the configuration when the mode is ModeActive.
func (s Snapshot) Active() (Active, bool) {
	if s.p == nil {
		return Active{}, false
	}
	return s.p.active, s.p.mode == ModeActive
}

// Failure answers a copy of the field errors when the mode is Failed.
func (s Snapshot) Failure() ([]formschema.FieldError, bool) {
	if s.p == nil {
		return nil, false
	}
	return slices.Clone(s.p.failure), s.p.mode == Failed
}

// Server is the server section in force: Active's, Unconfigured's defaults,
// or in Failed the section when its own parse succeeded; false only in Failed
// when it did not. A false Server is still the fail-closed value: no trusted
// proxy, and the host policy of the entries as written, or one denying every
// Host when the file failed at its root.
func (s Snapshot) Server() (Server, bool) {
	if s.p == nil {
		return denyAllServer(), false
	}
	return s.p.server, s.p.serverOK
}
