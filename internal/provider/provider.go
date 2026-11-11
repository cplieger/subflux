// Package provider is the boundary between subflux and the subtitle sites it
// searches. An adapter sees only its Client, whose one HTTP path pins every
// request to the entry's Hosts, retries a GET or HEAD on a transient failure
// only, and classifies every non-2xx answer into a *Failure; Build wraps the
// adapter so every error leaving it is a redacted *Failure and every answer is
// bounded before the engine ranks it or makes a handle.
package provider

import (
	"context"
	"net/http"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/rank"
)

// ProviderID names one provider in settings, state and the wire.
type ProviderID string //nolint:revive // design 5's name; provider.ID would read as a provider's own id field

// Entry is one row of the provider table.
type Entry struct {
	Languages language.ProviderMap
	// Quirk, nil for none, sees each non-2xx answer with at most 4 KiB of its
	// body and returns a *Failure, or nil for the default classification.
	Quirk func(*http.Response, []byte) error
	New   func(Client, formschema.Values) (Adapter, error)
	// Data, nil for none, takes the engine's report for data the entry
	// downloads: the entry calls report with that data's condition at once
	// and when each refresh of it ends, and a later Data call replaces the
	// earlier report.
	Data func(report func(text string, active bool))
	ID   ProviderID
	// Summary is one English line of 1 to 120 bytes on what the provider
	// covers and whether it needs an account.
	Summary string
	// UserAgent, "" for subflux's own, replaces it on every request of an
	// Exception whose ruling allows a browser agent; Build refuses it on any
	// other entry.
	UserAgent string
	// Hosts is every host name the adapter may reach, exact names only.
	Hosts  []string
	Schema formschema.Schema
	// Exception marks a scraping adapter the user named, which the Client
	// holds to robots.txt, 4 requests a call and an hour's back-off on a 403,
	// 429 or 503.
	Exception bool
}

// Config is one provider's settings section.
type Config struct {
	Values   formschema.Values
	ID       ProviderID
	Priority int
	Enabled  bool
}

// Adapter is what Build returns. Search returns every candidate it read; when
// a later page failed it returns them with that page's *Failure, so the funnel
// observes the failure and no no-result evidence is booked from a partial
// answer.
type Adapter interface {
	Search(ctx context.Context, q Query) ([]Candidate, error)
	Fetch(ctx context.Context, ref Ref) ([]byte, error)
}

// Tester is implemented by a credentialed adapter: the cheapest call that
// proves its credential.
type Tester interface {
	Test(ctx context.Context) error
}

// Query is built once per run. For an episode External holds the series'
// ids; Year is the root's (0 unknown); Titles are the root's title then its
// alternative titles; Langs are this provider's tags from the query union;
// OSHash is the video's OpenSubtitles hash, zero when unknown.
type Query struct {
	Titles    []string
	Langs     []language.Tag
	External  media.ExternalIDs
	OSHash    uint64
	Numbering media.Numbering
	Year      int
	Absolute  int
	Kind      media.Kind
}

// Candidate is one result. Title is the provider's name for the media it
// matched ("" when it matched by id) and Year its year (0 when none); Covers
// is the numbering the provider claims, nil for none. An adapter sets Covers
// with MatchedBy ID only when a published mapping placed the file on those
// episodes, since search then reads them instead of the name.
type Candidate struct {
	Ref     Ref
	Lang    language.Tag
	Release string
	Title   string
	Covers  []media.Numbering
	Year    int
	FPS     float64
	Flags   language.Flags
	// MatchedBy is how the provider matched the query.
	MatchedBy rank.MatchedBy
	// Unconfirmed marks a file found for the query's media that the adapter
	// cannot place on its episode; search offers it for manual search only.
	Unconfirmed bool
	// MachineTranslated: the provider says a machine or an AI made it.
	MachineTranslated bool
}

// Ref is the adapter's own token for one download. It is held server-side
// and resolves only on the adapter's Hosts.
type Ref struct{ token string }

// NewRef wraps an adapter's token.
func NewRef(token string) Ref { return Ref{token: token} }

// Token is the adapter's token.
func (r Ref) Token() string { return r.token }

// MaxBody is the ceiling on every body the Client reads.
const MaxBody = 10 << 20

// The wrapper's answer bounds: candidates past MaxAnswer are dropped, a
// candidate whose Ref token is empty or over MaxRefToken bytes or with more
// than MaxCovers entries is dropped whole, and Release and Title are
// sanitized to one line of at most MaxText bytes.
const (
	MaxAnswer   = 500
	MaxRefToken = 1 << 10
	MaxText     = 512
	MaxCovers   = 64
)

// BoundText is s as the answer bounds keep a Release or Title: one line of
// at most MaxText bytes; cut reports that s changed.
func BoundText(s string) (text string, cut bool) {
	return textsafe.SanitizeSingleLineCapped(s, MaxText, "")
}

// maxSkipCause bounds a Skipped's Cause.
const maxSkipCause = 120

// Skipped is Search's error when the adapter sends no request because the
// query lacks what it searches by. Cause is one English line naming what it
// lacks. The wrapper passes it through when no request was sent and makes it
// Permanent otherwise.
type Skipped struct{ Cause string } //nolint:errname // design 5 names it Skipped: a search not sent, not a fault

func (s *Skipped) Error() string { return "skipped: " + s.Cause }
