package status

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AlertSource names one alert slot. It comes from a fixed constant below or
// from a constructor, never from a literal.
type AlertSource string

// The fixed sources, each its own kind.
const (
	AlertConfig         AlertSource = "config"
	AlertStore          AlertSource = "store"
	AlertJobRecords     AlertSource = "job_records"
	AlertBackup         AlertSource = "backup"
	AlertAuthDisabled   AlertSource = "auth_disabled"
	AlertAdminSocket    AlertSource = "admin_socket"
	AlertRestartPending AlertSource = "restart_pending"
	AlertPendingWrites  AlertSource = "pending_writes"
)

// MaxAlertSource bounds a source's bytes.
const MaxAlertSource = 512

// AlertKind is a per-entity alert kind, the one its constructor names. Only
// the values below exist outside this package, so RetainAlerts is never given
// a kind no constructor yields; the zero AlertKind is none of them.
type AlertKind struct{ name string }

// String is the kind's name, the prefix of its sources.
func (k AlertKind) String() string { return k.name }

// The per-entity kinds, one per constructor.
var (
	ProviderAlerts      = AlertKind{"provider"}
	ProviderDataAlerts  = AlertKind{"provider_data"}
	SourceAlerts        = AlertKind{"source"}
	SourceOverlapAlerts = AlertKind{"source_overlap"}
	MediaServerAlerts   = AlertKind{"media_server"}
	MediaRootAlerts     = AlertKind{"media_root"}
)

var (
	fixedKinds  = []AlertSource{AlertConfig, AlertStore, AlertJobRecords, AlertBackup, AlertAuthDisabled, AlertAdminSocket, AlertRestartPending, AlertPendingWrites}
	entityKinds = []AlertKind{ProviderAlerts, ProviderDataAlerts, SourceAlerts, SourceOverlapAlerts, MediaServerAlerts, MediaRootAlerts}
)

// The per-entity constructors return "<kind>:<value>" when that is at most
// MaxAlertSource bytes of one line of printable UTF-8, else
// "<kind>#<hex SHA-256 of value>". The separators keep the two forms
// disjoint, so distinct values give distinct sources; the readable value
// belongs in the alert's text.

// ProviderAlert is the source for a subtitle provider, by its id.
func ProviderAlert(id string) AlertSource { return entitySource(ProviderAlerts, id) }

// ProviderDataAlert is the source for the data a subtitle provider
// downloads, such as AnimeTosho's anime map, by the provider's id.
func ProviderDataAlert(id string) AlertSource { return entitySource(ProviderDataAlerts, id) }

// SourceAlert is the source for a media source, by its id.
func SourceAlert(id string) AlertSource { return entitySource(SourceAlerts, id) }

// SourceOverlapAlert is the source for a media source whose roots overlap
// another's, by its id.
func SourceOverlapAlert(id string) AlertSource { return entitySource(SourceOverlapAlerts, id) }

// MediaServerAlert is the source for a media server, by its id.
func MediaServerAlert(id string) AlertSource { return entitySource(MediaServerAlerts, id) }

// MediaRootAlert is the source for a media root, by its path.
func MediaRootAlert(path string) AlertSource { return entitySource(MediaRootAlerts, path) }

func entitySource(kind AlertKind, value string) AlertSource {
	if s := kind.name + ":" + value; len(s) <= MaxAlertSource && printableLine(value) {
		return AlertSource(s)
	}
	sum := sha256.Sum256([]byte(value))
	return AlertSource(kind.name + "#" + hex.EncodeToString(sum[:]))
}

func printableLine(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

// AlertKindID splits a source into the labels its gauge series carries: its
// kind, a fixed source's own name or its constructor's kind, and its id, the
// text after the kind's separator (the value, or the digest of a hashed
// source), empty for a fixed source. It is false for a source no constant or
// constructor yields.
func AlertKindID(s AlertSource) (kind, id string, ok bool) {
	if slices.Contains(fixedKinds, s) {
		return string(s), "", true
	}
	for _, k := range entityKinds {
		if v, ok := strings.CutPrefix(string(s), k.name+":"); ok && entitySource(k, v) == s {
			return k.name, v, true
		}
		if h, ok := strings.CutPrefix(string(s), k.name+"#"); ok && isDigest(h) {
			return k.name, h, true
		}
	}
	return "", "", false
}

// isDigest reports whether h is a lowercase hex SHA-256, as hex.EncodeToString
// writes it.
func isDigest(h string) bool {
	return len(h) == 2*sha256.Size && !strings.ContainsFunc(h, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	})
}
