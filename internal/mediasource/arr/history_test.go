package arr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

func seriesRef(id int) mediasource.NativeRef {
	return mediasource.NativeRef{ID: arr.Native(id), Kind: media.Episode}
}

// fakeHistory answers like arrapi's HistorySince: every record dated at or
// after the bound's second, oldest first.
type fakeHistory struct {
	recs  []arr.Record
	calls int
}

func (f *fakeHistory) commit(id, series int, date time.Time) {
	f.recs = append(f.recs, arr.Record{ID: id, Root: seriesRef(series), Date: date})
}

func (f *fakeHistory) deleteSeries(series int) {
	f.recs = slices.DeleteFunc(f.recs, func(r arr.Record) bool { return r.Root == seriesRef(series) })
}

func (f *fakeHistory) read(_ context.Context, since time.Time) ([]arr.Record, error) {
	f.calls++
	from := since.Truncate(time.Second)
	var out []arr.Record
	for _, r := range f.recs {
		if !r.Date.Before(from) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b arr.Record) int { return a.Date.Compare(b.Date) })
	return out, nil
}

// poller carries one source's cursor across Changes calls.
type poller struct {
	t   *testing.T
	h   arr.History
	log *slog.Logger
	cur mediasource.Cursor
}

func newPoller(t *testing.T, h arr.History) (*poller, *capture.Recorder) {
	log, rec := capture.New()
	return &poller{t: t, h: h, log: log}, rec
}

func (p *poller) poll() []mediasource.NativeRef {
	p.t.Helper()
	changes, next, err := arr.Changes(p.t.Context(), p.h, p.cur, p.log)
	if err != nil {
		p.t.Fatalf("Changes: %v", err)
	}
	p.cur = next
	roots := make([]mediasource.NativeRef, len(changes))
	for i, c := range changes {
		roots[i] = c.Root
	}
	return roots
}

func (p *poller) cursor() []byte {
	b, _ := p.cur.MarshalBinary()
	return b
}

func series(ids ...int) []mediasource.NativeRef {
	out := make([]mediasource.NativeRef, len(ids))
	for i, id := range ids {
		out[i] = seriesRef(id)
	}
	return out
}

func wantRoots(t *testing.T, when string, got []mediasource.NativeRef, ids ...int) {
	t.Helper()
	if want := series(ids...); !slices.Equal(got, want) {
		t.Errorf("%s: Changes reported %v, want %v", when, got, want)
	}
}

// quiet polls every 30 s for d, failing on any report.
func (p *poller) quiet(d time.Duration) {
	p.t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); {
		time.Sleep(30 * time.Second)
		if got := p.poll(); len(got) != 0 {
			p.t.Errorf("a quiet poll at %s reported %v", time.Now().UTC().Format(time.TimeOnly), got)
		}
	}
}

func TestLateRecordReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &fakeHistory{}
		p, _ := newPoller(t, h.read)
		p.poll()
		time.Sleep(30 * time.Second)
		h.commit(1, 1, time.Now())
		wantRoots(t, "the first import", p.poll(), 1)
		time.Sleep(30 * time.Second)
		h.commit(2, 2, time.Now().Add(-90*time.Second))
		wantRoots(t, "a record dated 90 s before the cursor", p.poll(), 2)
		p.quiet(10 * time.Minute)
	})
}

func TestIDCommittedOutOfOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &fakeHistory{}
		p, _ := newPoller(t, h.read)
		p.poll()
		time.Sleep(30 * time.Second)
		date := time.Now()
		h.commit(102, 2, date)
		wantRoots(t, "102 visible first", p.poll(), 2)
		time.Sleep(30 * time.Second)
		h.commit(101, 1, date.Add(-time.Second))
		wantRoots(t, "101 committed after 102", p.poll(), 1)
		p.quiet(10 * time.Minute)
	})
}

func TestBurstThenQuietReportsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &fakeHistory{}
		p, _ := newPoller(t, h.read)
		p.poll()
		time.Sleep(time.Minute)
		start := time.Now().Add(-time.Minute)
		ids := make([]int, 600)
		for i := range ids {
			ids[i] = i + 1
			h.commit(i+1, i+1, start.Add(time.Duration(i)*100*time.Millisecond))
		}
		wantRoots(t, "the burst", p.poll(), ids...)
		p.quiet(10 * time.Minute)
	})
}

func TestHeadAfterBurstReportsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &fakeHistory{}
		start := time.Now()
		time.Sleep(2 * time.Minute)
		for i := range 300 {
			h.commit(i+1, i+1, start.Add(time.Duration(i)*400*time.Millisecond))
		}
		p, _ := newPoller(t, h.read)
		wantRoots(t, "the head", p.poll())
		p.quiet(10 * time.Minute)
	})
}

func TestEmptyHistoryHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &fakeHistory{}
		p, _ := newPoller(t, h.read)
		wantRoots(t, "the head", p.poll())
		stored := p.cursor()
		if len(stored) == 0 {
			t.Fatal("the head answered the zero cursor, which asks for the head again")
		}
		var restored mediasource.Cursor
		if err := restored.UnmarshalBinary(stored); err != nil {
			t.Fatal(err)
		}
		p.cur = restored
		time.Sleep(30 * time.Second)
		h.commit(1, 1, time.Now())
		wantRoots(t, "the first import", p.poll(), 1)
	})
}

func TestWindowBoundRecordNotReReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &fakeHistory{}
		p, _ := newPoller(t, h.read)
		p.poll()
		time.Sleep(10 * time.Second)
		bound := time.Now()
		h.commit(1, 1, bound)
		time.Sleep(20 * time.Second)
		wantRoots(t, "the import", p.poll(), 1)
		time.Sleep(2 * time.Minute)
		h.commit(2, 2, bound.Add(2*time.Minute+500*time.Millisecond))
		wantRoots(t, "the import whose window starts 0.5 s after the first", p.poll(), 2)
		p.quiet(10 * time.Minute)
	})
}

func TestSinceNeverMovesBack(t *testing.T) {
	t.Run("a deleted series' records", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := &fakeHistory{}
			p, _ := newPoller(t, h.read)
			p.poll()
			time.Sleep(30 * time.Second)
			h.commit(1, 1, time.Now().Add(-time.Minute))
			h.commit(2, 2, time.Now())
			wantRoots(t, "two imports", p.poll(), 1, 2)
			before := p.cursor()
			time.Sleep(30 * time.Second)
			h.deleteSeries(2)
			wantRoots(t, "after the newest series is deleted", p.poll())
			if !bytes.Equal(p.cursor(), before) {
				t.Error("the cursor moved when the newest record vanished")
			}
		})
	})
	t.Run("the newest row deleted before the advance", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := &fakeHistory{}
			p, _ := newPoller(t, h.read)
			p.poll()
			time.Sleep(30 * time.Second)
			h.commit(1, 1, time.Now())
			wantRoots(t, "the import", p.poll(), 1)
			before := p.cursor()
			time.Sleep(3 * time.Minute)
			h.deleteSeries(1)
			wantRoots(t, "3 minutes later, the row deleted", p.poll())
			if !bytes.Equal(p.cursor(), before) {
				t.Error("an empty answer before since + 4 min changed the cursor")
			}
		})
	})
}

func TestFutureRecordDoesNotPoisonCursor(t *testing.T) {
	const ahead = "ahead of the clock"
	t.Run("in a poll", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := &fakeHistory{}
			p, rec := newPoller(t, h.read)
			p.poll()
			time.Sleep(30 * time.Second)
			before := p.cursor()
			due := time.Now().Add(24 * time.Hour)
			h.commit(1, 1, due)
			wantRoots(t, "a row dated a day ahead", p.poll())
			if !bytes.Equal(p.cursor(), before) {
				t.Error("the row dated a day ahead moved the cursor")
			}
			if n := rec.CountLevel(slog.LevelWarn, ahead); n != 1 {
				t.Errorf("WARN records = %d, want 1", n)
			}
			time.Sleep(30 * time.Second)
			h.commit(2, 2, time.Now())
			wantRoots(t, "a later import", p.poll(), 2)
			time.Sleep(time.Until(due))
			wantRoots(t, "the row once its date comes", p.poll(), 1)
			p.quiet(5 * time.Minute)
		})
	})
	t.Run("in the head", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := &fakeHistory{}
			due := time.Now().Add(24 * time.Hour)
			h.commit(1, 1, due)
			p, rec := newPoller(t, h.read)
			wantRoots(t, "the head", p.poll())
			if n := rec.CountLevel(slog.LevelWarn, ahead); n != 1 {
				t.Errorf("WARN records = %d, want 1", n)
			}
			time.Sleep(30 * time.Second)
			h.commit(2, 2, time.Now())
			wantRoots(t, "an import after the head", p.poll(), 2)
			time.Sleep(time.Until(due))
			wantRoots(t, "the row once its date comes", p.poll(), 1)
		})
	})
}

func TestChangesNilLogIsDefault(t *testing.T) {
	rec := capture.Default(t)
	h := &fakeHistory{}
	h.commit(1, 1, time.Now().Add(24*time.Hour))
	if _, _, err := arr.Changes(t.Context(), h.read, mediasource.Cursor{}, nil); err != nil {
		t.Fatalf("Changes = %v", err)
	}
	if n := rec.CountLevel(slog.LevelWarn, "ahead of the clock"); n != 1 {
		t.Errorf("slog.Default WARN records = %d, want 1", n)
	}
}

// TestQuietAdvanceClockSkewResidual pins the advance's documented residual:
// an import made right after it by an arr whose clock runs over 4 minutes
// behind is left to the next whole pass.
func TestQuietAdvanceClockSkewResidual(t *testing.T) {
	for name, tc := range map[string]struct {
		behind time.Duration
		want   []int
	}{
		"3 minutes behind": {3 * time.Minute, []int{1}},
		"5 minutes behind": {5 * time.Minute, nil},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := &fakeHistory{}
				p, _ := newPoller(t, h.read)
				p.poll()
				p.quiet(2 * time.Minute)
				time.Sleep(10 * time.Second)
				h.commit(1, 1, time.Now().Add(-tc.behind))
				var got []mediasource.NativeRef
				for range 20 {
					time.Sleep(30 * time.Second)
					got = append(got, p.poll()...)
				}
				wantRoots(t, "the import after the advance, over 10 minutes", got, tc.want...)
			})
		})
	}
}

func TestUnknownCursorVersionExpired(t *testing.T) {
	for name, raw := range map[string][]byte{
		"another version":     {2, 0},
		"no since":            {1},
		"a truncated seen id": {1, 0, 0x80},
	} {
		t.Run(name, func(t *testing.T) {
			var cur mediasource.Cursor
			if err := cur.UnmarshalBinary(raw); err != nil {
				t.Fatal(err)
			}
			h := &fakeHistory{}
			changes, next, err := arr.Changes(t.Context(), h.read, cur, slog.New(slog.DiscardHandler))
			if se := asSourceError(t, err); se.Cause != mediasource.Expired {
				t.Errorf("cause = %v, want expired", se.Cause)
			}
			if b, _ := next.MarshalBinary(); changes != nil || len(b) != 0 {
				t.Errorf("Changes = %v, cursor %x; want nothing", changes, b)
			}
			if h.calls != 0 {
				t.Errorf("history read %d times for a cursor it cannot use", h.calls)
			}
		})
	}
}

func asSourceError(t *testing.T, err error) *mediasource.SourceError {
	t.Helper()
	se, ok := err.(*mediasource.SourceError)
	if !ok {
		t.Fatalf("error = %v (%T), want a *mediasource.SourceError", err, err)
	}
	return se
}

// memSonarr is an in-memory Sonarr 5 serving history as Sonarr does: every
// row dated at or after the query's date, oldest first. It records the rows
// and bytes of each history answer.
type memSonarr struct {
	rows    []historyRow
	served  []int
	bytes   []int
	endless bool
	status  int
}

type historyRow struct {
	Date   time.Time `json:"date"`
	Event  string    `json:"eventType"`
	ID     int       `json:"id"`
	Series int       `json:"seriesId"`
}

func (m *memSonarr) commit(id, series int, event string, date time.Time) {
	m.rows = append(m.rows, historyRow{Date: date.UTC(), Event: event, ID: id, Series: series})
}

func (m *memSonarr) RoundTrip(r *http.Request) (*http.Response, error) {
	var body io.Reader
	switch r.URL.Path {
	case "/api":
		body = strings.NewReader(`{"current":"v5","deprecated":["v3"]}`)
	case "/api/v5/system/status":
		body = strings.NewReader(`{"appName":"Sonarr","version":"` + v5 + `"}`)
	case "/api/v5/history/since":
		if m.status != 0 {
			return m.answer(r, m.status, strings.NewReader(`{}`)), nil
		}
		body = m.history(r)
	default:
		return m.answer(r, http.StatusNotFound, strings.NewReader(`{}`)), nil
	}
	return m.answer(r, http.StatusOK, body), nil
}

func (*memSonarr) answer(r *http.Request, code int, body io.Reader) *http.Response {
	return &http.Response{
		StatusCode:    code,
		Header:        http.Header{"X-Application-Version": {v5}, "Content-Type": {"application/json"}},
		Body:          io.NopCloser(body),
		ContentLength: -1,
		Request:       r,
	}
}

func (m *memSonarr) history(r *http.Request) io.Reader {
	m.bytes = append(m.bytes, 0)
	if m.endless {
		m.served = append(m.served, -1)
		return &endlessList{n: &m.bytes[len(m.bytes)-1]}
	}
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("date"))
	if err != nil {
		panic(err)
	}
	var out []historyRow
	for _, row := range m.rows {
		if !row.Date.Before(since) {
			out = append(out, row)
		}
	}
	slices.SortStableFunc(out, func(a, b historyRow) int { return a.Date.Compare(b.Date) })
	b, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	if out == nil {
		b = []byte("[]")
	}
	m.served = append(m.served, len(out))
	m.bytes[len(m.bytes)-1] = len(b)
	return bytes.NewReader(b)
}

// endlessList is a JSON array of imports that never ends, counting what is
// read of it.
type endlessList struct {
	n    *int
	rest []byte
}

func (e *endlessList) Read(p []byte) (int, error) {
	if *e.n == 0 {
		e.rest = []byte("[")
	}
	read := 0
	for read < len(p) {
		if len(e.rest) == 0 {
			e.rest = []byte(`{"date":"2000-01-01T00:00:00Z","eventType":"downloadFolderImported","id":1,"seriesId":1},`)
		}
		c := copy(p[read:], e.rest)
		e.rest = e.rest[c:]
		read += c
	}
	*e.n += read
	return read, nil
}

func memClient(t *testing.T, m *memSonarr) *sonarr.Client {
	t.Helper()
	c, err := sonarr.New("http://sonarr.test", testKey,
		sonarr.WithHTTPClient(&http.Client{Transport: m}),
		sonarr.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// sonarrHistory is the Sonarr adapter's reader: HistorySince with its four
// kinds, each record mapped to its series.
func sonarrHistory(c *sonarr.Client) arr.History {
	return func(ctx context.Context, since time.Time) ([]arr.Record, error) {
		recs, err := c.HistorySince(ctx, since,
			sonarr.EventDownloadImported, sonarr.EventFolderImported, sonarr.EventFileRenamed, sonarr.EventFileDeleted)
		if err != nil {
			return nil, err
		}
		out := make([]arr.Record, 0, len(recs))
		for _, r := range recs {
			if r.SeriesID > 0 {
				out = append(out, arr.Record{Date: r.Date, Root: seriesRef(r.SeriesID), ID: r.ID})
			}
		}
		return out, nil
	}
}

func TestEventKinds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &memSonarr{}
		p, _ := newPoller(t, sonarrHistory(memClient(t, m)))
		p.poll()
		time.Sleep(30 * time.Second)
		now := time.Now()
		m.commit(1, 1, "episodeFileRenamed", now)
		m.commit(2, 2, "episodeFileDeleted", now)
		m.commit(3, 3, "seriesFolderImported", now)
		m.commit(4, 4, "grabbed", now)
		for id := range 10 {
			m.commit(10+id, 5, "downloadFolderImported", now)
		}
		wantRoots(t, "a rename, a delete, a folder import, a grab and ten imports", p.poll(), 1, 2, 3, 5)
	})
}

func TestHistoryReadFailureClassified(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &memSonarr{}
		p, _ := newPoller(t, sonarrHistory(memClient(t, m)))
		p.poll()
		m.status = http.StatusUnauthorized
		_, next, err := arr.Changes(t.Context(), p.h, p.cur, p.log)
		if se := asSourceError(t, err); se.Cause != mediasource.Unauthorized {
			t.Errorf("cause = %v, want unauthorized", se.Cause)
		}
		if b, _ := next.MarshalBinary(); len(b) != 0 {
			t.Errorf("a failed read answered cursor %x, want none", b)
		}
	})
}

func TestQuietAfterBurstBandwidth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &memSonarr{}
		p, _ := newPoller(t, sonarrHistory(memClient(t, m)))
		p.poll()
		start := time.Now()
		time.Sleep(2 * time.Minute)
		// Sonarr dates carry .NET's 100 ns ticks, finer than the cursor's ms.
		for i := range 5000 {
			m.commit(i+1, i%50+1, "downloadFolderImported", start.Add(time.Duration(i)*24*time.Millisecond+3456700))
		}
		if got := p.poll(); len(got) != 50 {
			t.Fatalf("the burst reported %d roots, want 50", len(got))
		}
		burstAt := len(m.served)
		for poll := range 12 {
			time.Sleep(30 * time.Second)
			if got := p.poll(); len(got) != 0 {
				t.Errorf("quiet poll %d reported %v", poll+1, got)
			}
		}
		quiet := m.served[burstAt:]
		t.Logf("records per quiet poll %v, bytes %v", quiet, m.bytes[burstAt:])
		for i, n := range quiet {
			want := 5000
			if i >= 8 {
				want = 0
			}
			if n != want {
				t.Errorf("quiet poll %d was served %d records, want %d", i+1, n, want)
			}
		}
		time.Sleep(30 * time.Second)
		m.commit(5001, 99, "downloadFolderImported", time.Now())
		wantRoots(t, "an import after the advance", p.poll(), 99)
		p.quiet(5 * time.Minute)
	})
}

func TestStaleCursorPastCapExpires(t *testing.T) {
	const listCap = 64 << 20
	t.Run("a month-old cursor over a history past the cap", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m := &memSonarr{}
			p, _ := newPoller(t, sonarrHistory(memClient(t, m)))
			p.poll()
			time.Sleep(30 * 24 * time.Hour)
			m.endless = true
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			_, next, err := arr.Changes(ctx, p.h, p.cur, p.log)
			if se := asSourceError(t, err); se.Cause != mediasource.Expired {
				t.Errorf("cause = %v (%v), want expired", se.Cause, se.Err)
			}
			if b, _ := next.MarshalBinary(); len(b) != 0 {
				t.Errorf("an expired read answered cursor %x, want none", b)
			}
			for i, n := range m.bytes[1:] {
				if n > listCap+1 {
					t.Errorf("history answer %d was read for %d bytes, past the %d cap", i+1, n, listCap)
				}
			}
		})
	})
	t.Run("a month-quiet cursor", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m := &memSonarr{}
			p, _ := newPoller(t, sonarrHistory(memClient(t, m)))
			p.poll()
			time.Sleep(30 * 24 * time.Hour)
			wantRoots(t, "a month later", p.poll())
			time.Sleep(30 * time.Second)
			m.commit(1, 1, "downloadFolderImported", time.Now())
			wantRoots(t, "the next import", p.poll(), 1)
		})
	})
}

func TestCursorEncodesEverySeenID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &fakeHistory{}
		p, _ := newPoller(t, h.read)
		p.poll()
		time.Sleep(30 * time.Second)
		big := int(^uint32(0)) + 7
		h.commit(big, 1, time.Now())
		h.commit(3, 2, time.Now())
		wantRoots(t, "ids "+strconv.Itoa(big)+" and 3", p.poll(), 1, 2)
		time.Sleep(30 * time.Second)
		wantRoots(t, "the same ids re-read", p.poll())
	})
}
