package mediawrite_test

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/go/httpwire/httpwiretest"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

// failureArm is one way an api-called export refuses, and the code api
// answers for it.
type failureArm struct {
	call func() error
	name string
	want string
}

// failureArms builds a library with one root of each state and drives
// every refusal of the api-called exports.
func failureArms(t *testing.T) []failureArm {
	t.Helper()
	dir := t.TempDir()
	up, down := filepath.Join(dir, "up"), filepath.Join(dir, "down")
	writeFile(t, filepath.Join(up, "keep"), "x")
	served := filepath.Join(up, "S01E03.mkv")
	writeFile(t, served, "video")
	writeFile(t, filepath.Join(up, "S01E03.en.srt"), srtOf("en"))
	writeFile(t, filepath.Join(up, "S01E03.en.sub"), "vobsub")
	writeFile(t, filepath.Join(up, "S01E03.fr.srt"), srtOf("fr"))
	store := openStore(t)
	insts := seed(t, store, filepath.Join(dir, "elsewhere", "S01E01.mkv"), filepath.Join(down, "S01E02.mkv"), served)
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(up, down)}, nil)
	run(t, w)
	if err := store.BeginDelete(t.Context(), insts[2], "S01E03.fr.srt", sha256.Sum256([]byte(srtOf("fr")))); err != nil {
		t.Fatal(err)
	}
	ref := func(inst statedb.InstanceID, name string) mediawrite.FileRef {
		r, err := mediawrite.DecodeRef(inst, name)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	en, sub, pending := ref(insts[2], "S01E03.en.srt"), ref(insts[2], "S01E03.en.sub"), ref(insts[2], "S01E03.fr.srt")
	hashed := mediawrite.Expectation{SHA256: sha256.Sum256([]byte(srtOf("en")))}
	subHash := mediawrite.Expectation{SHA256: sha256.Sum256([]byte("vobsub"))}
	retime := func(r mediawrite.FileRef, e mediawrite.Expectation, tm timing.Timing) func() error {
		return func() error { _, err := w.Retime(t.Context(), r, e, tm, nil); return err }
	}
	current := func(r mediawrite.FileRef, e mediawrite.Expectation) func() error {
		return func() error { _, err := w.Current(t.Context(), r, e); return err }
	}
	del := func(r mediawrite.FileRef, e mediawrite.Expectation) func() error {
		return func() error { return w.Delete(t.Context(), r, e) }
	}
	shift := timing.Shift{Ms: 1000}
	openVideo := func(inst statedb.InstanceID) func() error {
		return func() error {
			f, err := w.OpenVideo(t.Context(), inst)
			if err == nil {
				_ = f.Close()
			}
			return err
		}
	}
	list := func(inst statedb.InstanceID) func() error {
		return func() error {
			_, err := w.List(t.Context(), inst, mediawrite.ListOptions{})
			return err
		}
	}
	return []failureArm{
		{name: "OpenVideo unknown instance", call: openVideo(insts[1] + 100), want: "not_found"},
		{name: "OpenVideo outside every root", call: openVideo(insts[0]), want: "outside_root"},
		{name: "OpenVideo root unavailable", call: openVideo(insts[1]), want: "media_root_unavailable"},
		{name: "List unknown instance", call: list(insts[1] + 100), want: "not_found"},
		{name: "List outside every root", call: list(insts[0]), want: "outside_root"},
		{name: "List root unavailable", call: list(insts[1]), want: "media_root_unavailable"},
		{name: "DecodeRef zero instance", call: func() error { _, err := mediawrite.DecodeRef(0, "a.en.srt"); return err }, want: "not_found"},
		{name: "DecodeRef bad name", call: func() error { _, err := mediawrite.DecodeRef(insts[0], "../a.en.srt"); return err }, want: "not_found"},
		{name: "ValidName", call: func() error { return mediawrite.ValidName("a.vtt") }, want: "not_found"},
		{name: "Retime zero reference", call: retime(mediawrite.FileRef{}, hashed, shift), want: "not_found"},
		{name: "Retime outside every root", call: retime(ref(insts[0], "S01E01.en.srt"), hashed, shift), want: "outside_root"},
		{name: "Retime root unavailable", call: retime(ref(insts[1], "S01E02.en.srt"), hashed, shift), want: "media_root_unavailable"},
		{name: "Retime zero expectation", call: retime(en, mediawrite.Expectation{}, shift), want: "stale_version"},
		{name: "Retime invalid timing", call: retime(en, hashed, timing.Shift{Ms: 7 * 3_600_000}), want: "timing_invalid"},
		{name: "Retime pending", call: retime(pending, hashed, shift), want: "write_pending"},
		{name: "Retime .sub", call: retime(sub, subHash, shift), want: "format_unsupported"},
		{name: "Delete zero reference", call: del(mediawrite.FileRef{}, hashed), want: "not_found"},
		{name: "Delete outside every root", call: del(ref(insts[0], "S01E01.en.srt"), hashed), want: "outside_root"},
		{name: "Delete root unavailable", call: del(ref(insts[1], "S01E02.en.srt"), hashed), want: "media_root_unavailable"},
		{name: "Delete zero expectation", call: del(en, mediawrite.Expectation{}), want: "stale_version"},
		{name: "Delete pending", call: del(pending, hashed), want: "write_pending"},
		{name: "Delete .sub", call: del(sub, subHash), want: "format_unsupported"},
		{name: "Current zero reference", call: current(mediawrite.FileRef{}, hashed), want: "not_found"},
		{name: "Current outside every root", call: current(ref(insts[0], "S01E01.en.srt"), hashed), want: "outside_root"},
		{name: "Current root unavailable", call: current(ref(insts[1], "S01E02.en.srt"), hashed), want: "media_root_unavailable"},
		{name: "Current zero expectation", call: current(en, mediawrite.Expectation{}), want: "stale_version"},
		{name: "Current pending", call: current(pending, hashed), want: "write_pending"},
		{name: "Current .sub", call: current(sub, subHash), want: "format_unsupported"},
	}
}

// codeOf is the code of the first entry of mediawrite's, then timing's,
// Owner.Codes that err matches by errors.Is, "" when none does: the lookup
// httpwire's answer mapping makes for a row whose owners are those two.
func codeOf(err error) string {
	for _, c := range slices.Concat(mediawrite.Owner.Codes, timing.Owner.Codes) {
		if errors.Is(err, c.Err) {
			return c.Code
		}
	}
	return ""
}

func TestCodesCoverFailureTable(t *testing.T) {
	t.Parallel()
	for _, arm := range failureArms(t) {
		err := arm.call()
		if err == nil {
			t.Errorf("%s: succeeded, want a refusal", arm.name)
			continue
		}
		if got := codeOf(err); got != arm.want {
			t.Errorf("%s: %v answers code %q, want %q", arm.name, err, got, arm.want)
		}
	}
}

func TestOwnerSentinels(t *testing.T) {
	t.Parallel()
	httpwiretest.Sentinels(t, mediawrite.Owner)
}
