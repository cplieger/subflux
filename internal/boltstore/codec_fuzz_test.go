package boltstore

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
)

// sampleRecords returns one fully-populated value of each core-domain record
// type, used as the FuzzDecode seed corpus.
func sampleRecords() []any {
	base := time.Date(2025, 6, 1, 12, 30, 0, 0, time.UTC)
	return []any{
		&attemptRec{LastTried: base, NextRetry: base.Add(time.Hour), Failures: 3},
		&stateRec{
			ID: 4242, MediaType: api.MediaTypeEpisode, MediaID: "tt0903747-s01e01",
			Language: "fr", Variant: api.VariantStandard,
			Provider:    api.ProviderNameOpenSubtitles,
			ReleaseName: "Show.S01E01.1080p.WEB-DL", Path: "/media/tv/Show/Show.S01E01.fr.srt",
			Title: "Show", ImdbID: "tt0903747", ReleaseTag: "WEB-DL",
			Score: 92, Season: 1, Episode: 1, Manual: true,
			VideoPath: "/media/tv/Show/Show.S01E01.mkv", MediaImported: base,
		},
		&fileRec{Codec: "subrip", UpdatedAt: base},
		&scanRec{Title: "Inception", AudioLang: "en", Season: 0, Episode: 0, ScannedAt: base},
	}
}

// FuzzDecode fuzzes the record decoder against arbitrary bytes. The properties
// it enforces, for every core-domain record type:
//   - the decoder never panics on any input;
//   - invalid JSON fails closed (FailClosed returns an error) and is skipped
//     under TolerantSkip;
//   - any input that decodes cleanly round-trips (re-encode then re-decode is
//     byte-stable).
func FuzzDecode(f *testing.F) {
	for _, rec := range sampleRecords() {
		if enc, err := kv.Encode(rec); err == nil {
			f.Add(enc)
		}
	}
	f.Add([]byte(""))
	f.Add([]byte("{"))
	f.Add([]byte("not json"))
	f.Add([]byte("null"))
	f.Add([]byte("{}"))
	f.Add([]byte("[]"))
	f.Add([]byte(`{"failures":99}`))
	f.Add([]byte(`{"id":1,"score":-5,"manual":true}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzOne[attemptRec](t, bucketSearchAttempts, data)
		fuzzOne[stateRec](t, bucketSubtitleState, data)
		fuzzOne[fileRec](t, bucketSubtitleFiles, data)
		fuzzOne[scanRec](t, bucketScanState, data)
	})
}

// fuzzOne applies the FuzzDecode properties to a single record type.
func fuzzOne[T any](t *testing.T, bucket string, data []byte) {
	t.Helper()
	valid := json.Valid(data)

	// FailClosed: never skips; invalid JSON must error; no panic.
	var v T
	skip, err := decodeRecord(kv.FailClosed, bucket, []byte("k"), data, &v)
	if skip {
		t.Fatalf("[%s] FailClosed must never skip", bucket)
	}
	if !valid && err == nil {
		t.Fatalf("[%s] invalid JSON %q decoded without error", bucket, data)
	}

	// TolerantSkip: never errors; invalid JSON is skipped; no panic.
	var vs T
	skip, serr := decodeRecord(kv.TolerantSkip, bucket, []byte("k"), data, &vs)
	if serr != nil {
		t.Fatalf("[%s] TolerantSkip returned error: %v", bucket, serr)
	}
	if !valid && !skip {
		t.Fatalf("[%s] invalid JSON %q not skipped under TolerantSkip", bucket, data)
	}

	if err != nil {
		// Malformed for this type: handled (errored/skipped). Done.
		return
	}

	// Decoded cleanly -> round-trip must be byte-stable.
	enc, eerr := encodeRecord(&v)
	if eerr != nil {
		t.Fatalf("[%s] re-encode failed: %v", bucket, eerr)
	}
	var v2 T
	if _, err := decodeRecord(kv.FailClosed, bucket, []byte("k"), enc, &v2); err != nil {
		t.Fatalf("[%s] re-decode of re-encoded value failed: %v", bucket, err)
	}
	enc2, eerr := encodeRecord(&v2)
	if eerr != nil {
		t.Fatalf("[%s] second re-encode failed: %v", bucket, eerr)
	}
	if !bytes.Equal(enc, enc2) {
		t.Fatalf("[%s] round-trip not stable:\n first %s\n second %s", bucket, enc, enc2)
	}
}
