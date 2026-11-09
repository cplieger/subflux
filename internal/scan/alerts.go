package scan

import (
	"errors"
	"strings"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
)

// errNoVerdict ends a tick that says nothing about the source's health: a
// cancelled or cut pass, a job that could not be recorded, a source the Set
// no longer holds. It leaves source:<id> as it was.
var errNoVerdict = errors.New("scan: the tick reached no verdict")

// reconcileSource sets source:<id> after a tick (design 5.4): raised while
// the tick ended on a *SourceError other than Expired, while the latest
// whole pass failed on one and none has completed since, or while a root
// waits in the retry set. A tick ending on a storage error or a cancel
// neither raises nor clears it.
func (l *loop) reconcileSource(tickErr error) {
	var cause *mediasource.SourceError
	if tickErr != nil {
		se, ok := errors.AsType[*mediasource.SourceError](tickErr)
		if !ok {
			return
		}
		if se.Cause != mediasource.Expired {
			cause = se
		}
	}
	if cause == nil {
		cause = l.wholeSourceErr
	}
	if cause == nil {
		cause = l.latestRetryErr()
	}
	text := ""
	if cause != nil {
		text = sourceWords(l.src, cause)
	}
	l.s.status.Reconcile(status.SourceAlert(string(l.src)), cause != nil, text)
}

// latestRetryErr is the failure of the retry set's most recently failed
// root, the first in ref order among those failed at once, so the alert's
// text holds still between ticks; nil when the set is empty.
func (l *loop) latestRetryErr() *mediasource.SourceError {
	var latest retryRoot
	var latestRef mediasource.NativeRef
	for ref, r := range l.retry {
		if latest.err == nil || r.at.After(latest.at) || (r.at.Equal(latest.at) && compareRefs(ref, latestRef) < 0) {
			latest, latestRef = r, ref
		}
	}
	return latest.err
}

// sourceWords is source:<id>'s one line: the source, the cause in words and
// the failing request.
func sourceWords(src media.SourceID, se *mediasource.SourceError) string {
	if ae, ok := errors.AsType[*mediasource.AliasedPathError](se); ok {
		return string(src) + " reports " + ae.Path + " under " + ae.To + ", which a mapping already serves: map that folder too or remove the overlap"
	}
	if ae, ok := errors.AsType[*mediasource.AiredOrderError](se); ok {
		return string(src) + " lists " + ae.Title + " in another episode order, and its aired order cannot be read now: " + ae.Err.Error()
	}
	var words string
	switch se.Cause {
	case mediasource.Unauthorized:
		words = "refused the API key"
	case mediasource.NotFound:
		words = "does not answer as its API at this URL (a 404, or a web page): check the URL's base path"
	case mediasource.Protocol:
		words = "answered something subflux cannot read"
	default:
		words = "cannot be reached"
	}
	line := string(src) + " " + words
	if se.Op != "" {
		line += "; failing request: " + se.Op
	}
	if se.Err != nil {
		line += ": " + se.Err.Error()
	}
	return line
}

// reconcileOverlap sets source_overlap:<id> after a FinishPass that
// returned no error: raised while owners, every other source owning what
// this one reports, is not empty.
func (l *loop) reconcileOverlap(owners []media.SourceID) {
	text := ""
	if len(owners) > 0 {
		names := make([]string, len(owners))
		for i, o := range owners {
			names[i] = string(o)
		}
		verb := "owns"
		if len(names) > 1 {
			verb = "own"
		}
		text = string(l.src) + " reports videos or episodes that " + joinNames(names) + " already " + verb +
			"; nothing is attached for them. Map the two sources to different folders, or remove one."
	}
	l.s.status.Reconcile(status.SourceOverlapAlert(string(l.src)), len(owners) > 0, text)
}

// joinNames is "a", "a and b", or "a, b and c".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
