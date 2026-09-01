package archive

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/subtitleext"
)

// bombRatio is the largest expansion a member may declare. Above it the entry
// is skipped rather than read: subtitle text compresses well but not that
// well, and the ratio is the cheapest decompression-bomb guard available
// before any read.
const bombRatio = 50

// member is one archive entry the selection loop can consider without knowing
// which container produced it.
type member struct {
	// read returns the member's content. For a streamed container it is only
	// valid while this member is current, so the selection loop calls it
	// before advancing.
	read func() ([]byte, error)

	name string

	// packed and unpacked are the sizes the container declares, the input to
	// the bomb guard. sizeUnknown means it declared none, or declared one
	// this program cannot represent, which is itself a reason to skip the
	// entry.
	packed      int64
	unpacked    int64
	sizeUnknown bool
	isDir       bool
}

// declaredSize converts a container's unsigned size, reporting whether it
// fits. A crafted archive can declare a size above math.MaxInt64, and a
// silent wrap would turn a bomb into a negative number the ratio guard reads
// as harmless.
func declaredSize(n uint64) (int64, bool) {
	if n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func gate(m member) bool {
	if m.isDir {
		return false
	}
	if !subtitleext.ArchiveInput(filepath.Ext(m.name)) {
		return false
	}
	// Skips the __MACOSX/._name.srt noise a macOS-made archive carries.
	if strings.HasPrefix(filepath.Base(m.name), ".") {
		return false
	}
	if m.sizeUnknown || m.packed < 0 || m.unpacked < 0 {
		return false
	}
	if m.packed == 0 && m.unpacked > 0 {
		return false
	}
	if m.packed > 0 && m.unpacked/m.packed > bombRatio {
		return false
	}
	return true
}

// selectMember returns the content of the first gated member the target
// matches and the container can read.
//
// There is deliberately no fallback to a member the target did not match, so
// a season pack cannot yield the wrong episode. An Any target matches every
// member, so "find this episode" and "take the first subtitle" are one loop
// rather than two paths that have to agree.
//
// The three ways this fails are reported separately, because they call for
// opposite actions: nothing readable at all; members present but none
// claiming the episode, so the refusal lists what it saw; a member that
// matched and then failed to read, so the refusal carries that error rather
// than reporting no match.
func selectMember(members func(func(member) bool), want epmarker.Target) ([]byte, error) {
	sel := selection{want: want}
	for m := range members {
		if !gate(m) {
			continue
		}
		if content, found := sel.consider(m); found {
			return content, nil
		}
	}
	return sel.result()
}

// selection carries the accounting one walk over an archive's members builds
// up. A miss has to be classified from the whole walk, so the state outlives
// each member and cannot live in the loop body.
type selection struct {
	names    []string
	readErrs []error
	bareSeen []int
	bareHit  []byte
	want     epmarker.Target
}

func (s *selection) consider(m member) (content []byte, found bool) {
	// Only the last path element decides the episode: a directory named for
	// one season must not answer for a member inside it.
	base := filepath.Base(m.name)
	s.names = append(s.names, m.name)

	if s.want.Matches(base) {
		got, err := m.read()
		if err == nil {
			return got, true
		}
		s.readErrs = append(s.readErrs, err)
		return nil, false
	}

	if ep, ok := epmarker.BareEpisode(base); ok {
		s.bareSeen = append(s.bareSeen, ep)
	}
	// Hold the first member that states the target's episode as a bare
	// number. Read it now because a streamed container only permits that
	// while the member is current, and result discards it unless the whole
	// archive turns out to name no season anywhere.
	if s.bareHit == nil && s.want.MatchesBareEpisode(base) {
		if got, err := m.read(); err == nil {
			s.bareHit = got
		}
	}
	return nil, false
}

func (s *selection) result() ([]byte, error) {
	// The bare-number reading is sound only for an archive where nothing
	// claims a readable marker: the season comes from the search rather than
	// from any name (see Target.MatchesBareEpisode), so a notation that DOES
	// parse always wins.
	if s.bareHit != nil && claimSummary(s.names) == "" {
		return s.bareHit, nil
	}

	switch {
	case len(s.readErrs) > 0:
		return nil, &memberReadError{want: s.want, errs: s.readErrs}
	case len(s.names) == 0:
		return nil, errors.New("archive holds no subtitle member")
	default:
		// Members were present and none matched, which only a fixed target
		// can produce: an Any target matches everything, so it would have
		// tried to read one and landed in the first arm.
		return nil, noMatchRefusal(s.want, s.names, s.bareSeen)
	}
}

// noMatchRefusal says why nothing in the archive answered, in terms the
// operator has to act on: a pack for another season/episode means get a
// different release; bare episode numbers with no season means the episode
// is absent, not misread; no episode notation at all means this is a
// subflux gap rather than a release problem.
func noMatchRefusal(want epmarker.Target, names []string, bareSeen []int) error {
	if claimed := claimSummary(names); claimed != "" {
		return fmt.Errorf("archive holds no member for %s; its %d member(s) claim %s: %s",
			want.String(), len(names), claimed, summarizeNames(names))
	}
	if len(bareSeen) > 0 {
		return fmt.Errorf("archive holds no member for %s; its %d member(s) state bare episode "+
			"numbers %s with no season: %s",
			want.String(), len(names), summarizeInts(bareSeen), summarizeNames(names))
	}
	return fmt.Errorf("archive names no episode this scanner can read, so %s cannot be "+
		"matched from it; its %d member(s): %s", want.String(), len(names), summarizeNames(names))
}

func summarizeInts(nums []int) string {
	shown := nums
	if len(shown) > maxNamesInError {
		shown = shown[:maxNamesInError]
	}
	parts := make([]string, 0, len(shown)+1)
	for _, n := range shown {
		parts = append(parts, strconv.Itoa(n))
	}
	if len(nums) > len(shown) {
		parts = append(parts, fmt.Sprintf("(+%d more)", len(nums)-len(shown)))
	}
	return strings.Join(parts, ", ")
}

// claimSummary renders the distinct episodes the members claim. Empty when
// nothing claims an episode, which is the signal noMatchRefusal keys on.
func claimSummary(names []string) string {
	var all []epmarker.Marker
	for _, n := range names {
		for _, m := range epmarker.Claims(filepath.Base(n)) {
			if !slices.Contains(all, m) {
				all = append(all, m)
			}
		}
	}
	if len(all) == 0 {
		return ""
	}
	shown := all
	if len(shown) > maxNamesInError {
		shown = shown[:maxNamesInError]
	}
	parts := make([]string, 0, len(shown)+1)
	for _, m := range shown {
		parts = append(parts, m.String())
	}
	if len(all) > len(shown) {
		parts = append(parts, fmt.Sprintf("(+%d more)", len(all)-len(shown)))
	}
	return strings.Join(parts, ", ")
}

// memberReadError is the set of read failures for members that DID match the
// target: the archive is the right one and something about the entries
// defeated the reader.
//
// It renders on one line rather than using errors.Join, because this text
// lands in a slog attribute and Join's newlines would split one log record
// into several.
//
// provider.ExtractAndValidate marks every refusal from this package
// permanent, so an unreadable member (a property of the archive) is never
// retried as if it were transient.
type memberReadError struct {
	errs []error
	want epmarker.Target
}

func (e *memberReadError) Error() string {
	parts := make([]string, 0, len(e.errs))
	for _, err := range e.errs {
		parts = append(parts, err.Error())
	}
	return fmt.Sprintf("every member matching %s failed to read: %s",
		e.want.String(), strings.Join(parts, "; "))
}

// Unwrap exposes the per-member causes so errors.Is reaches a sentinel such as
// zip.ErrAlgorithm.
func (e *memberReadError) Unwrap() []error { return e.errs }
