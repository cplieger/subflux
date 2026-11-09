package statedb

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

const (
	formatGolden = "testdata/format.golden"
	regenerate   = "UPDATE_GOLDEN=1 go test ./internal/statedb/ -run TestFormatGolden"
)

// goldenLine is one pinned encoding: a kind (digest, key, value, index or
// time), the name it belongs to, the codec version it was written under
// (0 where none applies) and its bytes.
type goldenLine struct {
	kind, name string
	bytes      []byte
	version    uint32
}

func (l goldenLine) id() string { return l.kind + " " + l.name }

// emptyBytes spells no bytes, so every line keeps four fields.
const emptyBytes = "-"

func (l goldenLine) String() string {
	b := hex.EncodeToString(l.bytes)
	if b == "" {
		b = emptyBytes
	}
	return fmt.Sprintf("%s %s %d %s", l.kind, l.name, l.version, b)
}

// TestFormatGolden pins core's and auth's stored format from the production
// codecs: a byte change under an unchanged codec version is a silent format
// change a reopen would misread, and a version bump with no byte change
// resets every user's namespace for nothing.
func TestFormatGolden(t *testing.T) {
	t.Run("every hash and handle is stored", func(t *testing.T) {
		for _, f := range authFixtures(t) {
			stored := string(f.key) + string(f.value)
			for _, h := range f.held {
				if !strings.Contains(stored, h) {
					t.Errorf("%s stores neither key nor value holding %q", f.name, h)
				}
			}
		}
	})
	got := formatLines(t)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		var b strings.Builder
		for _, l := range got {
			b.WriteString(l.String() + "\n")
		}
		if err := os.WriteFile(formatGolden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(formatGolden)
	if err != nil {
		t.Fatalf("read %s: %v; write it with %s", formatGolden, err, regenerate)
	}
	want, err := parseGolden(raw)
	if err != nil {
		t.Fatalf("%s: %v; rewrite it with %s", formatGolden, err, regenerate)
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g.id()] = true
		w, ok := want[g.id()]
		if !ok {
			t.Errorf("%s is not in %s; review the new encoding, then run %s", g.id(), formatGolden, regenerate)
			continue
		}
		if msg := goldenDrift(w, g); msg != "" {
			t.Errorf("%s: %s", g.id(), msg)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s is in %s but no longer produced; run %s", id, formatGolden, regenerate)
		}
	}
}

// goldenDrift names what changed between the pinned line w and g, "" for
// nothing.
func goldenDrift(w, g goldenLine) string {
	sameBytes, sameVersion := bytes.Equal(w.bytes, g.bytes), w.version == g.version
	switch {
	case sameBytes && sameVersion:
		return ""
	case g.kind == "digest" || g.kind == "index" || g.kind == "time":
		return fmt.Sprintf("changed from %x to %x; review it, then run %s", w.bytes, g.bytes, regenerate)
	case sameVersion:
		return fmt.Sprintf("bytes changed from %x to %x under version %d; bump the codec's version", w.bytes, g.bytes, g.version)
	case sameBytes:
		return fmt.Sprintf("version moved from %d to %d with no byte change; revert the bump", w.version, g.version)
	}
	return fmt.Sprintf("version %d to %d and bytes changed; review the new encoding, then run %s", w.version, g.version, regenerate)
}

func parseGolden(raw []byte) (map[string]goldenLine, error) {
	out := map[string]goldenLine{}
	for n, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d has %d fields, want 4", n+1, len(f))
		}
		v, err := strconv.ParseUint(f[2], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("line %d: version: %w", n+1, err)
		}
		var b []byte
		if f[3] != emptyBytes {
			b, err = hex.DecodeString(f[3])
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: bytes: %w", n+1, err)
		}
		l := goldenLine{kind: f[0], name: f[1], version: uint32(v), bytes: b}
		if _, dup := out[l.id()]; dup {
			return nil, fmt.Errorf("line %d repeats %s", n+1, l.id())
		}
		out[l.id()] = l
	}
	return out, nil
}

// formatLines is each namespace's digest, each fixture row's key and value,
// every index entry those rows produce, and the time component of instants
// on both sides of the epoch.
func formatLines(t *testing.T) []goldenLine {
	t.Helper()
	db, ts := openSchema(t)
	c, a := ts.core, ts.auth
	fixtures := allFixtures(t)
	if err := db.Update(t.Context(), func(tx *bolttable.Tx) error {
		for _, f := range fixtures {
			if err := f.put(tx, ts); err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
		}
		// The sparse indexes' rows: an unidentified sighting and an
		// ownerless instance.
		unidentified := itemRef{Source: "sonarr", Root: mediasource.NativeRef{Kind: media.Episode, ID: "73"}, Item: "4022"}
		if err := c.sightings.Put(tx, unidentified, &sightingRow{Title: "Three"}); err != nil {
			return err
		}
		return c.instances.Put(tx, 8, &instanceRow{Orphaned: fixtureAt, VideoPath: "/tv/Show/S01E03.mkv"})
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := db.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var lines []goldenLine
	for _, ns := range stats.Namespaces {
		lines = append(lines, goldenLine{kind: "digest", name: ns.Name, bytes: ns.Format[:]})
	}
	for _, f := range fixtures {
		lines = append(lines,
			goldenLine{kind: "key", name: f.name, version: f.keyVersion, bytes: f.key},
			goldenLine{kind: "value", name: f.name, version: f.valueVersion, bytes: f.value})
	}
	if err := db.View(t.Context(), func(r *bolttable.ReadTx) error {
		for _, ix := range []struct {
			name string
			scan func(*bolttable.ReadTx) ([][2][]byte, error)
		}{
			{"sightings.unidentified", entriesOf(c.unidentified)},
			{"instances.path", entriesOf(c.byPath)},
			{"instances.orphaned", entriesOf(c.orphaned)},
			{"downloads.item", entriesOf(c.itemHistory)},
			{"downloads.root", entriesOf(c.rootHistory)},
			{"work.order", entriesOf(c.workOrder)},
			{"jobs.key", entriesOf(c.jobByKey)},
			{"jobs.owner", entriesOf(c.jobsByOwner)},
			{"jobs.ended", entriesOf(c.jobsEnded)},
			{"jobs.owner_ended", entriesOf(c.ownerEnded)},
			{"jobs.user_ended", entriesOf(c.userEnded)},
			{"users.username", entriesOf(a.byUsername)},
			{"users.oidc", entriesOf(a.byOIDC)},
			{"users.handle", entriesOf(a.byHandle)},
			{"sessions.hash", entriesOf(a.sessionByHash)},
			{"sessions.id", entriesOf(a.sessionByID)},
			{"passkeys.credential", entriesOf(a.byCredential)},
			{"keys.hash", entriesOf(a.keyByHash)},
			{"keys.user", entriesOf(a.keysByUser)},
			{"tokens.subject", entriesOf(a.tokensBySub)},
			{"tokens.expires", entriesOf(a.tokensByExpiry)},
			{"tokens.issued_by", entriesOf(a.tokensByIssuer)},
			{"failures.unreported", entriesOf(a.unreported)},
			{"key_attributions.user", entriesOf(a.attrsByUser)},
		} {
			entries, err := ix.scan(r)
			if err != nil {
				return fmt.Errorf("%s: %w", ix.name, err)
			}
			for i, e := range entries {
				name := ix.name + "." + strconv.Itoa(i)
				lines = append(lines, goldenLine{kind: "index", name: name, bytes: e[0]})
				if len(e[1]) > 0 {
					lines = append(lines, goldenLine{kind: "index", name: name + ".projection", bytes: e[1]})
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{time.Unix(0, -1).UTC(), time.Unix(0, 0).UTC(), fixtureAt} {
		lines = append(lines, goldenLine{kind: "time", name: at.Format(time.RFC3339Nano), bytes: appendTime(nil, at)})
	}
	return lines
}

// entriesOf lists an index's entry keys and projections in index order.
func entriesOf[K, V any](ix *bolttable.Index[K, V]) func(*bolttable.ReadTx) ([][2][]byte, error) {
	return func(r *bolttable.ReadTx) ([][2][]byte, error) {
		var out [][2][]byte
		err := ix.Scan(r, bolttable.Range{}, func(ikey []byte, _ K, proj []byte) (bool, error) {
			out = append(out, [2][]byte{bytes.Clone(ikey), bytes.Clone(proj)})
			return true, nil
		})
		return out, err
	}
}

func TestGoldenDrift(t *testing.T) {
	pinned := goldenLine{kind: "value", name: "stamps", version: 1, bytes: []byte{1}}
	cases := []struct {
		name string
		g    goldenLine
		want string
	}{
		{name: "unchanged", g: pinned, want: ""},
		{name: "bytes under the same version", g: goldenLine{kind: "value", name: "stamps", version: 1, bytes: []byte{2}}, want: "bump the codec's version"},
		{name: "a bump with no byte change", g: goldenLine{kind: "value", name: "stamps", version: 2, bytes: []byte{1}}, want: "revert the bump"},
		{name: "both changed", g: goldenLine{kind: "value", name: "stamps", version: 2, bytes: []byte{2}}, want: regenerate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := goldenDrift(pinned, tc.g)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Errorf("goldenDrift = %q, want one naming %q", got, tc.want)
			}
		})
	}
}
