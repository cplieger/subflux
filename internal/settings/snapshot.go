// Package settings loads, holds and saves subflux's one configuration file:
// each section parsed by its owner, the result a Snapshot that is
// unconfigured, Failed or Active, never a configuration beside a flag.
package settings

import (
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/backup"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
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

// Registries is what the binary hands the section parsers: the adapter
// kinds each hand-walked section accepts, and the state store's directory,
// which backup.path may not name.
type Registries struct {
	StoreDir  string
	Sources   []mediasource.AdapterKind
	Providers []provider.Entry
}

// Active is a configuration that parsed, one field per section.
type Active struct {
	Languages language.Policy
	Server    Server
	Auth      accounts.Config
	Sources   []mediasource.Config
	Providers []provider.Config
	Embedded  embedded.Config
	Search    search.Config
	Backup    backup.Config
	Scan      mediasource.Schedule
	Media     mediawrite.Config
	Timing    timing.Config
}

// Snapshot is one immutable reading of the file. Only Load and Service.Save
// build one, so a mode and a configuration never disagree. The zero Snapshot
// has no mode, and its Server denies every Host.
type Snapshot struct{ p *snapshot }

type snapshot struct {
	// server is the section in force; serverOK is false only in Failed when
	// the section's own parse failed, and server is then its fail-closed value.
	server Server
	// auth is the section in force; authOK is false only in Failed when the
	// section did not parse, and auth is then the defaults.
	auth accounts.Config
	// fileAuth is the section as its own parse read it, kept when a cross
	// check or Boot refuses it; the defaults when that parse failed.
	fileAuth accounts.Config
	// doc is the file's mapping as written, before expansion, and view is
	// Document's node for it; both nil when there is no mapping.
	doc, view *yaml.Node
	revision  string
	failure   []formschema.FieldError
	file      fileState
	active    Active
	mode      Mode
	serverOK  bool
	authOK    bool
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

// Server is the server section in force: Active's, unconfigured's defaults,
// or in Failed the section when its own parse succeeded; false only in Failed
// when it did not. A false Server is still the fail-closed value with no
// trusted proxy; failClosedServer and denyAllServer own its host policy.
func (s Snapshot) Server() (Server, bool) {
	if s.p == nil {
		return denyAllServer(), false
	}
	return s.p.server, s.p.serverOK
}

// Auth is the auth section in force: Active's, unconfigured's defaults, or in
// Failed the section when it parsed and passed its cross check; false only in
// Failed when it did not, and the answer is then the defaults, which accounts
// runs with password sign-in refused to every client that is not local.
func (s Snapshot) Auth() (accounts.Config, bool) {
	if s.p == nil {
		return accounts.Config{}, false
	}
	return s.p.auth, s.p.authOK
}

// FileAuth is the auth section as config.yaml's own parse read it, the one
// the next boot decodes, in force or not: a section a cross check or Boot
// refused keeps its values, and one that failed to parse, or no file, reads
// as the defaults.
func (s Snapshot) FileAuth() accounts.Config {
	if s.p == nil {
		return accounts.Config{}
	}
	return s.p.fileAuth
}
