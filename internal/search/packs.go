package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/episodematch"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/release"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// aligned is one fetched payload judged for its trial's item: the aligned
// subtitle with the flags and facts it is delivered with, or the refusal
// its attempt row records and the error behind it.
type aligned struct {
	cause   error
	refusal string
	syncsched.Aligned
	facts rank.Facts
	flags language.Flags
}

// errNoMember is choose's answer when 8.11 gives the item no member.
var errNoMember = errors.New("no member is the item's")

// alignPayload decompresses payload, takes the item's member when it is an
// archive (8.11; bytes that are no archive are a single name) and aligns
// it into out. Its error is ctx's, or an Align failure that is no refusal.
func (e *Engine) alignPayload(ctx context.Context, t *trial, j *judged, payload []byte, out *aligned) error {
	*out = aligned{facts: j.facts, flags: j.cand.Flags}
	b, err := archive.Decompress(payload)
	if err != nil {
		out.refusal, out.cause = RefusalFormatUnsupported, err
		return nil
	}
	var ch episodematch.Choice
	var chosen bool
	member, err := archive.Extract(b, func(names []string) (int, error) {
		ch, chosen = t.choose(j.cand, names), true
		switch {
		case ch.Member >= 0:
			return ch.Member, nil
		case len(ch.Compare) > 0:
			return ch.Compare[0], nil
		}
		return 0, errNoMember
	})
	switch {
	case errors.Is(err, archive.ErrNotArchive):
		return e.alignOne(ctx, t, j, b, out, RefusalDifferentCut)
	case err != nil:
		out.refusal, out.cause = packRefusal(chosen, ch.Refusal), err
		return nil
	}
	out.facts.EpisodeByNumber = ch.ByNumber
	if len(ch.Compare) == 0 {
		out.flags = t.memberFlags(j.cand, ch.Member)
		cut := RefusalDifferentCut
		if ch.ByNumber {
			cut = RefusalDifferentEpisode
		}
		return e.alignOne(ctx, t, j, member, out, cut)
	}
	return e.compare(ctx, t, j, b, member, ch.Compare, out)
}

// alignOne aligns data into out with the plan out's facts and the pair's
// variant give; cut spells an ErrDifferentCut.
func (e *Engine) alignOne(ctx context.Context, t *trial, j *judged, data []byte, out *aligned, cut string) error {
	a, err := e.deps.Aligner.Align(ctx, t.r.inst.ID, data, j.cand.Lang, timing.Plan(out.facts, t.want.variant, e.timing))
	out.Aligned = a
	switch refusal := alignRefusal(err, cut); {
	case refusal != "":
		out.refusal, out.cause = refusal, err
	case err != nil && !errors.Is(err, syncsched.ErrSyncFailed):
		return err
	}
	return nil
}

// alignRefusal spells an Align error that refuses the attempt, "" for
// none; cut spells ErrDifferentCut.
func alignRefusal(err error, cut string) string {
	switch {
	case errors.Is(err, syncsched.ErrDifferentCut):
		return cut
	case errors.Is(err, timing.ErrFormatUnsupported):
		return RefusalFormatUnsupported
	}
	return ""
}

// packRefusal spells an archive that gave the item nothing: 8.11's count
// rule or an undetermined order when choose refused so, else the
// archive's refusal.
func packRefusal(chosen bool, r episodematch.Refusal) string {
	switch {
	case !chosen:
	case r == episodematch.EpisodeCount:
		return RefusalEpisodeCount
	case r == episodematch.OrderUndetermined:
		return RefusalOrderUndetermined
	}
	return RefusalFormatUnsupported
}

// compare is 8.11 step 6: every member of members, first already read,
// aligned against the reference into out, the most confident one taken;
// one trial whatever their number. A first answer with no reference makes
// the pack an undetermined order, every member refused makes it
// different_episode.
func (e *Engine) compare(ctx context.Context, t *trial, j *judged, b, first []byte, members []int, out *aligned) error {
	best := -1
	for k, m := range members {
		data := first
		if k > 0 {
			var err error
			if data, err = archive.Extract(b, func([]string) (int, error) { return m, nil }); err != nil {
				continue
			}
		}
		a, err := e.deps.Aligner.Align(ctx, t.r.inst.ID, data, j.cand.Lang, timing.Decision{Action: timing.ActionReference})
		switch {
		case alignRefusal(err, RefusalDifferentEpisode) != "":
			continue
		case err != nil && !errors.Is(err, syncsched.ErrSyncFailed):
			return err
		case k == 0 && a.Outcome.Method == timing.MethodNone:
			out.refusal = packRefusal(true, t.selectMembers(j.cand, t.names, true).Refusal)
			out.cause = errors.New("no reference track to compare the pack's members against")
			return nil
		case best < 0 || a.Outcome.Confidence > out.Outcome.Confidence:
			best, out.Aligned = m, a
		}
	}
	if best < 0 {
		out.refusal, out.cause = RefusalDifferentEpisode, fmt.Errorf("the reference refused all %d compared members", len(members))
		return nil
	}
	out.flags = t.memberFlags(j.cand, best)
	return nil
}

// choose is 8.11's member for the trial's item among an archive's gated
// names, which it keeps for compare: Select's choice for an episode, the
// first member serving the pair for a movie.
func (t *trial) choose(c *provider.Candidate, names []string) episodematch.Choice {
	t.names = make([]string, len(names))
	for i, n := range names {
		t.names[i] = boundedBase(n)
	}
	if len(t.r.subject.index) == 0 {
		for i := range t.names {
			if t.memberFlags(c, i).Serves(t.want.variant) {
				return episodematch.Choice{Member: i}
			}
		}
		return episodematch.Choice{Member: -1, Refusal: episodematch.NoMember}
	}
	return t.selectMembers(c, t.names, false)
}

// selectMembers runs Select over names for the trial's item.
func (t *trial) selectMembers(c *provider.Candidate, names []string, noReference bool) episodematch.Choice {
	members := make([]episodematch.Member, len(names))
	for i, n := range names {
		members[i] = episodematch.Member{Name: n, Flags: provider.Flags(c.Flags, n)}
	}
	return episodematch.Select(&episodematch.Input{
		Series: t.r.subject.seriesIn(c.Lang), Members: members, Item: t.r.subject.index[t.item],
		Variant: t.want.variant, Manual: t.manual, NoReference: noReference,
	})
}

// memberFlags are member i's flags: the candidate's, and what its name says.
func (t *trial) memberFlags(c *provider.Candidate, i int) language.Flags {
	return provider.Flags(c.Flags, t.names[i])
}

// boundedBase is name's last path element, at most release.MaxNameLen
// bytes of it, so a member name costs the readers no more than a release
// name does.
func boundedBase(name string) string {
	base := name[strings.LastIndexAny(name, `/\`)+1:]
	if len(base) <= release.MaxNameLen {
		return base
	}
	end := release.MaxNameLen
	for end > 0 && !utf8.RuneStart(base[end]) {
		end--
	}
	return base[:end]
}
