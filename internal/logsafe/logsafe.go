// Package logsafe binds runesafe's single-line preset to Subflux's log
// surface, so every untrusted string reaches slog through one policy.
//
// The threat is log-record forgery. A request-controlled value — a URL path, a
// query parameter, a JSON body field, an OIDC state, an error text that
// interpolates one of those — carries a raw newline straight through slog into
// the operator's log, where the reader cannot tell an injected line from one
// the server wrote. The other three runesafe classes ride the same channel: C1
// controls and ESC introduce terminal escape sequences that retitle a terminal
// or write to its clipboard, Bidi_Control runes reorder what a human reads
// without changing what compares, and U+2028/U+2029 split a record for a
// JavaScript viewer. Each becomes a space here, so the deception shows up as
// visible whitespace rather than vanishing along with the evidence of it.
//
// 256 bytes is the bound the provider layer already used for the same job, so
// adopting it here makes the number one constant instead of a literal repeated
// per call site. It is long enough for a media title or an upstream error
// sentence and short enough that a hostile value cannot push the useful
// attributes off the end of a log line.
//
// This is the emit-side half only. A field whose provenance is worth carrying
// through compute as well as emission takes runesafe.Untrusted at its decode
// struct instead, the way the opensubtitles and subdl responses do; reach for
// this package where the value arrives as a plain string from net/http and is
// logged near where it is read.
package logsafe

import "github.com/cplieger/runesafe/v2"

// MaxFieldBytes bounds one sanitized attribute.
const MaxFieldBytes = 256

// Field prepares one untrusted string for a slog attribute: runesafe's
// single-line preset, then a cap on a rune boundary with a "..." marker.
//
// Route every untrusted attribute through it rather than picking the ones that
// look dangerous. A reader of a handler should not have to prove which of four
// attributes is safe, and the cost is one call.
func Field(s string) string {
	return runesafe.SanitizeSingleLineBounded(s, MaxFieldBytes)
}
