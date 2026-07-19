//go:build corpus

// Golden-corpus arbitration harness (R4 of the sync-arbitration spec).
//
// Compares arbitration behavior over the 326-file benchmark corpus (dev-box
// media, assembled for the audio-sync research). The harness is a mandatory
// gate: under `-tags corpus` a missing SUBFLUX_CORPUS_DIR fails the run —
// it must never false-green. The committed manifest is part of the gate: it
// must declare its counts in the header and enumerate exactly the 326
// corpus cases (unique sorted stable IDs, lowercase SHA-256 input hashes,
// every no-reference non-run accounted against the declared count). A
// missing, partial, or malformed manifest FAILS the run — never skips,
// never passes vacuously on fewer rows. Without the tag this file is
// invisible to `go test ./...`.
//
// Two-commit protocol (baseline independence): the harness + manifest land
// as their own commit; running it there produces corpus-baseline.tsv (old
// arbitration). The arbitration change lands next; running again produces
// corpus-after.tsv. Both artifacts plus the diff attach to the PR. There is
// deliberately NO old-behavior flag inside one binary: a shared-helper
// refactor could silently stop representing old behavior.
//
// Commands (from the repo root; the corpus run needs ffmpeg/ffprobe and the
// dev-box media, and exceeds the default 10m test timeout):
//
//	SUBFLUX_CORPUS_DIR=/path/to/corpus go test -tags=corpus ./internal/subsync -run TestCorpusArbitration -count=1 -timeout=0
//
// First run on a new corpus (writes testdata/corpus-manifest.tsv, which is
// then committed):
//
//	SUBFLUX_CORPUS_DIR=... SUBFLUX_CORPUS_WRITE_MANIFEST=1 go test -tags=corpus ./internal/subsync -run TestCorpusArbitration -count=1 -timeout=0
//
// The per-file TSV (default corpus-result.tsv, override with
// SUBFLUX_CORPUS_OUT) records: stable ID, winner source (`none` admitted —
// the previously-applied-now-unapplied delta class), transform digest,
// calibrated confidence, alignment rating, and a score trace (per-candidate
// confidences + ratings, per-cluster membership) sufficient to prove each
// delta category from the traces, not guessed from rating deltas.
//
// Audit item riding the corpus run: constantOffsetConfidence calibration.
// The retired flat-0.6 no-split duplicate acted as a de facto floor at the
// sync_min_confidence boundary; if correct constant offsets now die at
// 0.55-0.6, recalibrate the curve — never resurrect the duplicate.
package subsync

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// corpusManifestPath is the committed manifest location, relative to the
// package directory (the test working directory).
const corpusManifestPath = "testdata/corpus-manifest.tsv"

// corpusCaseCount is the exact corpus size (R4.2): "all 326" means all 326
// individually accounted. A manifest with any other row count — including
// a short or truncated one — fails the gate rather than passing vacuously.
const corpusCaseCount = 326

// corpusManifestHeader is the count declaration the manifest header must
// carry (written by writeCorpusManifest, verified by loadCorpusManifest).
const corpusManifestHeader = "# subflux golden-corpus manifest: %d cases, %d no-reference"

// corpusGenProcedure names the manifest generation procedure quoted by
// every gate failure message.
const corpusGenProcedure = "generate the manifest once on the dev box with " +
	"SUBFLUX_CORPUS_DIR=<corpus-root> SUBFLUX_CORPUS_WRITE_MANIFEST=1 " +
	"go test -tags=corpus ./internal/subsync -run TestCorpusArbitration -count=1 -timeout=0" +
	", then commit testdata/corpus-manifest.tsv"

// corpusRefMarker values for the manifest reference column.
const (
	corpusRefEmbedded = "embedded"
	corpusRefNone     = "no-reference"
)

// videoExtensions are the container extensions scanned for corpus cases.
var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".ts": true, ".webm": true, ".m4v": true,
}

// corpusCase is one manifest row: a video file (the stable ID is its
// slash-normalized path relative to the corpus root) with a sibling
// subtitle `<stem>.srt` whose SHA-256 pins the arbitration input.
type corpusCase struct {
	id     string
	sha256 string
	ref    string
}

// subtitleRelPath derives the sibling subtitle path from the video ID.
func (c corpusCase) subtitleRelPath() string {
	return strings.TrimSuffix(c.id, filepath.Ext(c.id)) + ".srt"
}

func TestCorpusArbitration(t *testing.T) {
	root := os.Getenv("SUBFLUX_CORPUS_DIR")
	if root == "" {
		t.Fatal("SUBFLUX_CORPUS_DIR is required under -tags corpus (mandatory gate, no false green): " +
			"point it at the golden corpus root on the dev box")
	}

	if os.Getenv("SUBFLUX_CORPUS_WRITE_MANIFEST") == "1" {
		writeCorpusManifest(t, root)
	}

	cases := loadCorpusManifest(t)

	outPath := os.Getenv("SUBFLUX_CORPUS_OUT")
	if outPath == "" {
		outPath = "corpus-result.tsv"
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create %s: %v", outPath, err)
	}
	defer out.Close()
	w := bufio.NewWriter(out)
	fmt.Fprintln(w, "id\twinner_source\ttransform\tconfidence\trating\ttrace")

	var ran, noRef int
	for _, c := range cases {
		rec := runCorpusCase(t, root, c)
		if c.ref == corpusRefNone {
			noRef++
		} else {
			ran++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.4f\t%.4f\t%s\n",
			c.id, rec.winnerSource, rec.transform, rec.confidence, rec.rating, rec.trace)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush %s: %v", outPath, err)
	}

	t.Logf("corpus arbitration: %d cases (%d attempted, %d no-reference) -> %s",
		len(cases), ran, noRef, outPath)
}

// corpusRecord is one output TSV row.
type corpusRecord struct {
	winnerSource string
	transform    string
	trace        string
	confidence   float64
	rating       float64
}

// runCorpusCase verifies one case's input hash, extracts the embedded
// reference, runs the production arbitration, and captures the score trace.
// Case errors are reported through t.Errorf — the run completes with every
// non-run individually accounted, then fails (no silent skips).
func runCorpusCase(t *testing.T, root string, c corpusCase) corpusRecord {
	t.Helper()
	errRecord := func(reason string) corpusRecord {
		t.Errorf("corpus case %s: %s", c.id, reason)
		return corpusRecord{winnerSource: "none", transform: "none", trace: "error:" + reason}
	}

	subPath := filepath.Join(root, filepath.FromSlash(c.subtitleRelPath()))
	raw, err := os.ReadFile(subPath)
	if err != nil {
		return errRecord(fmt.Sprintf("read subtitle: %v", err))
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != c.sha256 {
		return errRecord(fmt.Sprintf("subtitle hash mismatch: manifest %s, disk %s (corpus drifted; regenerate the manifest)", c.sha256, got))
	}

	if c.ref == corpusRefNone {
		// Enumerated non-run: no usable embedded reference (the corpus
		// predates embedded-reference needs). Accounted, not attempted.
		return corpusRecord{winnerSource: "none", transform: "none", trace: corpusRefNone}
	}

	incCues, err := ParseSRT(strings.NewReader(string(NormalizeEncoding(raw))))
	if err != nil || len(incCues) < MinCuesForSync {
		return errRecord(fmt.Sprintf("subtitle unusable: err=%v cues=%d", err, len(incCues)))
	}

	ctx := context.Background()
	videoPath := filepath.Join(root, filepath.FromSlash(c.id))
	refCues, err := ExtractEmbeddedSRT(ctx, videoPath, "", "", nil)
	if err != nil || len(refCues) < MinCuesForSync {
		return errRecord(fmt.Sprintf("manifest says embedded reference but extraction failed: err=%v cues=%d", err, len(refCues)))
	}

	// Production arbitration path (reference strategies + vote; audio off).
	opts := DefaultSyncOptions()
	opts.VideoPath = videoPath
	winner := referenceSync(ctx, refCues, incCues, &opts)

	rec := corpusRecord{
		winnerSource: winner.Source.String(),
		transform:    winner.Transform.Digest(),
		confidence:   float64(winner.Confidence),
		rating:       alignmentRating(refCues, winner.Cues),
		trace:        corpusTrace(ctx, refCues, incCues, &opts),
	}
	if winner.Method == MethodNone {
		rec.winnerSource = "none"
	}
	return rec
}

// corpusTrace re-runs the four reference generators (they are deterministic
// for fixed inputs) and formats the per-candidate confidences/ratings and
// the validated cluster composition the vote saw.
func corpusTrace(ctx context.Context, reference, incorrect []Cue, opts *SyncOptions) string {
	candidates := []SyncResult{
		crossLangAlign(ctx, reference, incorrect),
		correctFramerate(ctx, reference, incorrect, opts.VideoPath),
		func() SyncResult {
			cues, offset := syncCues(ctx, reference, incorrect)
			r := SyncResult{
				Cues:      cues,
				Offset:    offset.Milliseconds(),
				Rate:      1.0,
				Method:    MethodOffset,
				Source:    SourceOffset,
				Transform: Transform{Kind: TransformShift, Shift: offset.Milliseconds()},
			}
			r.Confidence = constantOffsetConfidence(reference, incorrect, offset)
			return r
		}(),
		alignWithSplits(ctx, reference, incorrect, opts.SplitPenalty),
	}

	live := candidates[:0]
	for _, c := range candidates {
		if c.Confidence > ConfidenceNone {
			live = append(live, c)
		}
	}
	live = filterValidCandidates(live, incorrect)

	var parts []string
	for i := range live {
		parts = append(parts, fmt.Sprintf("%s:%.4f:r%.4f",
			live[i].Source, float64(live[i].Confidence), alignmentRating(reference, live[i].Cues)))
	}
	trace := "candidates=" + strings.Join(parts, "|")

	var clusterParts []string
	for _, cl := range clusterCandidates(live) {
		names := make([]string, len(cl.members))
		for i := range cl.members {
			names[i] = cl.members[i].Source.String()
		}
		clusterParts = append(clusterParts, "["+strings.Join(names, ",")+"]")
	}
	return trace + " clusters=" + strings.Join(clusterParts, "")
}

// loadCorpusManifest reads and validates the committed manifest against the
// R4.2 gate: a header declaring the case and no-reference counts, exactly
// corpusCaseCount well-formed rows with unique sorted IDs and lowercase
// SHA-256 input hashes, and a no-reference row count matching the declared
// one. Any shortfall fails the run: the mandatory gate must never pass
// vacuously on a missing, partial, or malformed manifest.
func loadCorpusManifest(t *testing.T) []corpusCase {
	t.Helper()
	f, err := os.Open(corpusManifestPath)
	if err != nil {
		t.Fatalf("open corpus manifest %s: %v (%s)", corpusManifestPath, err, corpusGenProcedure)
	}
	defer f.Close()

	var cases []corpusCase
	seen := make(map[string]bool, corpusCaseCount)
	declCases, declNoRef := -1, -1
	noRef := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "#") {
			var c, n int
			if _, err := fmt.Sscanf(text, corpusManifestHeader, &c, &n); err == nil {
				declCases, declNoRef = c, n
			}
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != 3 {
			t.Fatalf("%s:%d: want 3 tab-separated fields (id, sha256, reference), got %d (%s)",
				corpusManifestPath, line, len(fields), corpusGenProcedure)
		}
		if fields[0] == "" {
			t.Fatalf("%s:%d: empty id (%s)", corpusManifestPath, line, corpusGenProcedure)
		}
		if seen[fields[0]] {
			t.Fatalf("%s:%d: duplicate id %q — stable IDs must be unique (%s)",
				corpusManifestPath, line, fields[0], corpusGenProcedure)
		}
		seen[fields[0]] = true
		if !isCorpusSHA256(fields[1]) {
			t.Fatalf("%s:%d: sha256 %q is not 64 lowercase hex chars (%s)",
				corpusManifestPath, line, fields[1], corpusGenProcedure)
		}
		if fields[2] != corpusRefEmbedded && fields[2] != corpusRefNone {
			t.Fatalf("%s:%d: reference column %q, want %q or %q (%s)",
				corpusManifestPath, line, fields[2], corpusRefEmbedded, corpusRefNone, corpusGenProcedure)
		}
		if fields[2] == corpusRefNone {
			noRef++
		}
		cases = append(cases, corpusCase{id: fields[0], sha256: fields[1], ref: fields[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", corpusManifestPath, err)
	}
	if declCases < 0 {
		t.Fatalf("%s: missing the count declaration header (%q): every non-run must be declared up front (%s)",
			corpusManifestPath, corpusManifestHeader, corpusGenProcedure)
	}
	if len(cases) != corpusCaseCount {
		t.Fatalf("%s: %d rows, want exactly %d — the gate covers all %d corpus cases individually "+
			"and must not pass on a partial manifest (%s)",
			corpusManifestPath, len(cases), corpusCaseCount, corpusCaseCount, corpusGenProcedure)
	}
	if declCases != len(cases) {
		t.Fatalf("%s: header declares %d cases but %d rows follow (%s)",
			corpusManifestPath, declCases, len(cases), corpusGenProcedure)
	}
	if declNoRef != noRef {
		t.Fatalf("%s: header declares %d no-reference cases but rows mark %d (%s)",
			corpusManifestPath, declNoRef, noRef, corpusGenProcedure)
	}
	if !slices.IsSortedFunc(cases, func(a, b corpusCase) int { return strings.Compare(a.id, b.id) }) {
		t.Fatalf("%s: rows are not sorted by id (%s)", corpusManifestPath, corpusGenProcedure)
	}
	return cases
}

// isCorpusSHA256 reports whether s is a 64-char lowercase hex SHA-256 —
// the exact form runCorpusCase compares against (hex.EncodeToString).
func isCorpusSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// writeCorpusManifest scans the corpus root for video+subtitle pairs,
// probes each video for a usable embedded reference, and writes the sorted
// manifest. Run once per corpus on the dev box; commit the result.
func writeCorpusManifest(t *testing.T, root string) {
	t.Helper()
	var cases []corpusCase
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !videoExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		id := filepath.ToSlash(rel)
		c := corpusCase{id: id}

		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.subtitleRelPath())))
		if err != nil {
			// A video without a readable sibling subtitle is not a corpus
			// case; skip it rather than failing the scan.
			return nil
		}
		sum := sha256.Sum256(raw)
		c.sha256 = hex.EncodeToString(sum[:])

		refCues, err := ExtractEmbeddedSRT(context.Background(), path, "", "", nil)
		if err != nil || len(refCues) < MinCuesForSync {
			c.ref = corpusRefNone
		} else {
			c.ref = corpusRefEmbedded
		}
		cases = append(cases, c)
		return nil
	})
	if err != nil {
		t.Fatalf("scan corpus %s: %v", root, err)
	}
	slices.SortFunc(cases, func(a, b corpusCase) int { return strings.Compare(a.id, b.id) })

	var noRef int
	var sb strings.Builder
	for _, c := range cases {
		if c.ref == corpusRefNone {
			noRef++
		}
		fmt.Fprintf(&sb, "%s\t%s\t%s\n", c.id, c.sha256, c.ref)
	}
	header := fmt.Sprintf(corpusManifestHeader+"\n# id\tsubtitle_sha256\treference\n",
		len(cases), noRef)
	if err := os.WriteFile(corpusManifestPath, []byte(header+sb.String()), 0o600); err != nil {
		t.Fatalf("write %s: %v", corpusManifestPath, err)
	}
	t.Logf("wrote %s: %d cases (%d no-reference) — commit it", corpusManifestPath, len(cases), noRef)
}
