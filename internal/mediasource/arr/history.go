package arr

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Record is one history record as Changes reads it: a download import,
// folder import, rename or file delete of Root.
type Record struct {
	Date time.Time
	Root mediasource.NativeRef
	ID   int
}

// History reads every record dated at or after since in one request,
// oldest first: each adapter's HistorySince with its service's four kinds.
type History func(ctx context.Context, since time.Time) ([]Record, error)

const (
	cursorVersion = 1
	// overlap is how far before since each call re-reads, the slack a
	// record committed after a later-dated one is found within.
	overlap = 2 * time.Minute
	// advanceAfter: once the clock is this far past since with nothing
	// newer, a quiet cursor moves to now - overlap, so a burst's window is
	// not re-read on every poll.
	advanceAfter = 2 * overlap
)

// cursor is the decoded mediasource.Cursor: since, which never moves back,
// and seen, the id of every record dated in [since - overlap, since], the
// exact window the next call re-reads, so a record is reported at most
// once. seen has no count cap: one id per record of one answer, which
// arrapi caps at 64 MiB.
type cursor struct {
	since time.Time
	seen  []int64
}

var errCursorVersion = errors.New("the change cursor is from another version")

func (c cursor) encode() mediasource.Cursor {
	b := binary.AppendVarint([]byte{cursorVersion}, c.since.UnixMilli())
	for _, id := range c.seen {
		b = binary.AppendVarint(b, id)
	}
	var out mediasource.Cursor
	_ = out.UnmarshalBinary(b)
	return out
}

func decodeCursor(cur mediasource.Cursor) (cursor, bool, error) {
	b, _ := cur.MarshalBinary()
	if len(b) == 0 {
		return cursor{}, false, nil
	}
	if b[0] != cursorVersion {
		return cursor{}, true, errCursorVersion
	}
	ms, n := binary.Varint(b[1:])
	if n <= 0 {
		return cursor{}, true, errCursorVersion
	}
	c := cursor{since: time.UnixMilli(ms)}
	for rest := b[1+n:]; len(rest) > 0; {
		id, n := binary.Varint(rest)
		if n <= 0 {
			return cursor{}, true, errCursorVersion
		}
		c.seen = append(c.seen, id)
		rest = rest[n:]
	}
	return c, true, nil
}

// Changes answers an adapter's Changes over history, with one clock reading
// per call: a zero cursor takes the head and reports nothing; any other
// reports, once each, the root of every record in its re-read window whose
// id it has not seen. A record dated past the clock plus overlap counts for
// nothing until its date comes, with one WARN per call. An answer over
// arrapi's list cap, or an unreadable cursor, is Expired. A nil log is
// slog.Default.
func Changes(ctx context.Context, history History, cur mediasource.Cursor, log *slog.Logger) ([]mediasource.Change, mediasource.Cursor, error) {
	if log == nil {
		log = slog.Default()
	}
	now := time.Now().Truncate(time.Millisecond)
	c, held, err := decodeCursor(cur)
	if err != nil {
		return nil, mediasource.Cursor{}, sourceError(mediasource.Expired, err)
	}
	lower := now.Add(-overlap)
	if held {
		lower = c.since.Add(-overlap)
	}
	recs, err := history(ctx, lower)
	var tooLarge *arrapi.ResponseTooLargeError
	switch {
	case errors.As(err, &tooLarge):
		return nil, mediasource.Cursor{}, sourceError(mediasource.Expired, err)
	case err != nil:
		return nil, mediasource.Cursor{}, Classify(err)
	}
	recs = inWindow(recs, lower, now, log)
	latest := newest(recs)
	if !held {
		since := lower
		if latest.After(since) {
			since = latest
		}
		return nil, cursor{since: since, seen: seenIn(recs, since)}.encode(), nil
	}
	changes := unseen(recs, c.seen)
	since := c.since
	switch {
	case latest.After(since):
		since = latest
	case now.Sub(since) >= advanceAfter:
		since = now.Add(-overlap)
	case len(changes) == 0:
		return nil, cur, nil
	}
	return changes, cursor{since: since, seen: seenIn(recs, since)}.encode(), nil
}

// inWindow drops records dated before lower, which arrapi's second-precision
// query can return, and those dated past now + overlap, logging one WARN.
// Dates are cut to the cursor's millisecond, so a decoded since compares with
// them as the encoded one did; a finer date would read as newer than the
// since it set and move the cursor on every call.
func inWindow(recs []Record, lower, now time.Time, log *slog.Logger) []Record {
	limit := now.Add(overlap)
	out := recs[:0:0]
	ahead := 0
	for _, r := range recs {
		r.Date = r.Date.Truncate(time.Millisecond)
		switch {
		case r.Date.Before(lower):
		case r.Date.After(limit):
			ahead++
		default:
			out = append(out, r)
		}
	}
	if ahead > 0 {
		log.Warn("history records dated ahead of the clock left out until their date comes", "records", ahead)
	}
	return out
}

func newest(recs []Record) time.Time {
	var t time.Time
	for _, r := range recs {
		if r.Date.After(t) {
			t = r.Date
		}
	}
	return t
}

// seenIn answers the sorted ids of the records dated in [since - overlap,
// since].
func seenIn(recs []Record, since time.Time) []int64 {
	from := since.Add(-overlap)
	var ids []int64
	for _, r := range recs {
		if !r.Date.Before(from) && !r.Date.After(since) {
			ids = append(ids, int64(r.ID))
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// unseen names each distinct root of a record whose id is not in seen, in
// record order.
func unseen(recs []Record, seen []int64) []mediasource.Change {
	var out []mediasource.Change
	named := map[mediasource.NativeRef]bool{}
	for _, r := range recs {
		if _, ok := slices.BinarySearch(seen, int64(r.ID)); ok || named[r.Root] {
			continue
		}
		named[r.Root] = true
		out = append(out, mediasource.Change{Root: r.Root})
	}
	return out
}
