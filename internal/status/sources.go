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

// The per-entity kinds, one per constructor.
const (
	kindProvider      = "provider"
	kindSource        = "source"
	kindSourceOverlap = "source_overlap"
	kindMediaServer   = "media_server"
	kindMediaRoot     = "media_root"
)

var (
	fixedKinds  = []AlertSource{AlertConfig, AlertStore, AlertJobRecords, AlertBackup, AlertAuthDisabled, AlertAdminSocket, AlertRestartPending, AlertPendingWrites}
	entityKinds = []string{kindProvider, kindSource, kindSourceOverlap, kindMediaServer, kindMediaRoot}
)

// The per-entity constructors return "<kind>:<value>" when that is at most
// MaxAlertSource bytes of one line of printable UTF-8, else
// "<kind>#<hex SHA-256 of value>". The separators keep the two forms
// disjoint, so distinct values give distinct sources; the readable value
// belongs in the alert's text.

// ProviderAlert is the source for a subtitle provider, by its id.
func ProviderAlert(id string) AlertSource { return entitySource(kindProvider, id) }

// SourceAlert is the source for a media source, by its id.
func SourceAlert(id string) AlertSource { return entitySource(kindSource, id) }

// SourceOverlapAlert is the source for a media source whose roots overlap
// another's, by its id.
func SourceOverlapAlert(id string) AlertSource { return entitySource(kindSourceOverlap, id) }

// MediaServerAlert is the source for a media server, by its id.
func MediaServerAlert(id string) AlertSource { return entitySource(kindMediaServer, id) }

// MediaRootAlert is the source for a media root, by its path.
func MediaRootAlert(path string) AlertSource { return entitySource(kindMediaRoot, path) }

func entitySource(kind, value string) AlertSource {
	if s := kind + ":" + value; len(s) <= MaxAlertSource && printableLine(value) {
		return AlertSource(s)
	}
	sum := sha256.Sum256([]byte(value))
	return AlertSource(kind + "#" + hex.EncodeToString(sum[:]))
}

func printableLine(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

// AlertKind is a source's kind, the bounded label its gauge carries: a fixed
// source's own name, or its constructor's kind. It is false for a source no
// constant or constructor yields.
func AlertKind(s AlertSource) (string, bool) {
	if slices.Contains(fixedKinds, s) {
		return string(s), true
	}
	for _, k := range entityKinds {
		if v, ok := strings.CutPrefix(string(s), k+":"); ok && entitySource(k, v) == s {
			return k, true
		}
		if h, ok := strings.CutPrefix(string(s), k+"#"); ok && isDigest(h) {
			return k, true
		}
	}
	return "", false
}

// isDigest reports whether h is a lowercase hex SHA-256, as hex.EncodeToString
// writes it.
func isDigest(h string) bool {
	return len(h) == 2*sha256.Size && !strings.ContainsFunc(h, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	})
}
