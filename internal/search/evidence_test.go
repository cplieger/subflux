package search_test

import (
	"crypto/sha256"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
)

// rowTags are the target tags of rows naming key and id.
func rowTags(rows []statedb.NoResult, key media.Key, id string) []string {
	var out []string
	for _, r := range rows {
		if r.Key == key && r.Provider == id {
			out = append(out, r.Tag.String())
		}
	}
	slices.Sort(out)
	return out
}

func TestNoResultTagIsTarget(t *testing.T) {
	inRig(t, "close_varieties: true\ndefault: [{tag: nb}, {tag: pt-BR}]", func(t *testing.T, r *rig) {
		maps := r.provider("maps", "no", "pt-PT")
		items := []mediasource.Item{episode(1), episode(2), episode(3), episode(4)}
		insts := make([]statedb.InstanceID, len(items))
		for i := range items {
			insts[i] = r.seed(show, nil, items[i])[0]
		}
		for i := range 3 {
			r.pick(insts[i], statedb.Scan)
			if got := maps.lastQuery().Langs; !slices.Equal(got, []language.Tag{mustTag("no"), mustTag("pt-PT")}) {
				t.Errorf("episode %d asked %v, want the map's [no pt-PT]", i+1, got)
			}
		}
		rows := r.noResults(show, 1)
		for i := range 3 {
			if got := rowTags(rows, keyOf(t, show, items[i]), "maps"); !slices.Equal(got, []string{"nb", "pt-BR"}) {
				t.Errorf("episode %d's rows are under %v, want the targets [nb pt-BR]", i+1, got)
			}
		}
		calls := maps.calls()
		r.pick(insts[0], statedb.Scan)
		if maps.calls() != calls {
			t.Error("the next scan of episode 1 asked the provider its rows hold off")
		}
		r.pick(insts[3], statedb.Scan)
		if maps.calls() != calls {
			t.Error("episode 4 asked the provider three other episodes' rows hold off (the season stop)")
		}
		r.pick(insts[3], statedb.Item)
		if maps.calls() != calls+1 {
			t.Error("a user's run of episode 4 skipped the provider")
		}
	})
}

func TestOneProviderEmptyOthersStillAsked(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		empty := r.provider("empty", "en")
		other := r.provider("other", "en").answer(nil, cand(1, "en"))
		one := r.seed(show, nil, episode(1))[0]
		two := r.seed(show, nil, episode(2))[0]
		r.pick(one, statedb.Scan)
		r.pick(two, statedb.Scan)
		if empty.calls() != 2 || other.calls() != 2 {
			t.Errorf("calls empty %d, other %d over two episodes; want each asked for each", empty.calls(), other.calls())
		}
		r.pick(one, statedb.Scan)
		if empty.calls() != 2 || other.calls() != 3 {
			t.Errorf("rescanning episode 1: calls empty %d, other %d; want only the empty provider held off", empty.calls(), other.calls())
		}
	})
}

// attemptKey is CR-SE-7's attempt key of a candidate.
func attemptKey(id provider.ProviderID, c *provider.Candidate) [32]byte {
	return sha256.Sum256([]byte(keyenc.Join(string(id), c.Ref.Token())))
}

func TestTriedOnlyAnswerBacksOff(t *testing.T) {
	cases := []struct {
		edit  func(*provider.Candidate)
		name  string
		tried bool
	}{
		{name: "already tried", tried: true},
		{name: "conflicting", edit: func(c *provider.Candidate) { c.Release = "Dune.2021.HDCAM.x264-GRP" }},
		{name: "below the minimum fit", edit: func(c *provider.Candidate) { c.Release = "Dune.2021.720p.HDTV.x264-OTHER" }},
		{name: "manual only", edit: func(c *provider.Candidate) { c.Unconfirmed = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.cfg.MinFit = rank.SameSource
				offered := movieCand("en")
				if c.edit != nil {
					c.edit(&offered)
				}
				r.provider("p", "en").answer(nil, offered)
				item := movie()
				inst := r.seed(film, nil, item)[0]
				key := keyOf(t, film, item)
				if c.tried {
					err := r.db.RecordAttempts(t.Context(), []statedb.Download{{
						At: time.Now(), Target: mustTag("en"), Variant: language.Standard, Items: []media.Key{key},
						Inst: inst, Run: statedb.RunID{2}, Attempt: 1, Candidate: attemptKey("p", &offered), Refusal: "different_cut",
					}})
					if err != nil {
						t.Fatal(err)
					}
				}
				got := r.pick(inst, statedb.Scan)
				if p := pickOf(t, got, "en", language.Standard); len(p.Ranked) != 0 {
					t.Errorf("ranked %v, want nothing usable", releases(p))
				}
				if rows := rowTags(r.noResults(film, 0), key, "p"); !slices.Equal(rows, []string{"en"}) {
					t.Errorf("rows %v, want one en row", rows)
				}
			})
		})
	}
}

func TestNoResultVariantOrderFree(t *testing.T) {
	for _, answer := range []string{"hi", "forced"} {
		var rows [2][]string
		for i, order := range []string{"[standard, hi, forced]", "[forced, hi, standard]"} {
			t.Run(answer+" "+order, func(t *testing.T) {
				inRig(t, "default: [{tag: en, variants: "+order+"}]", func(t *testing.T, r *rig) {
					c := cand(1, "en", func(c *provider.Candidate) {
						c.Flags = language.Flags{HI: answer == "hi", Forced: answer == "forced"}
					})
					r.provider("p", "en").answer(nil, c)
					item := episode(1)
					r.pick(r.seed(show, nil, item)[0], statedb.Scan)
					rows[i] = rowTags(r.noResults(show, 1), keyOf(t, show, item), "p")
				})
			})
		}
		if !slices.Equal(rows[0], rows[1]) || len(rows[0]) != 0 {
			t.Errorf("an %s-only answer booked %v and %v under the two variant orders, want no row under either", answer, rows[0], rows[1])
		}
	}
}

func TestNoResultBookedOnlyForAskedEmpty(t *testing.T) {
	inRig(t, "default: [{tag: en}, {tag: fr}]", func(t *testing.T, r *rig) {
		r.provider("english", "en")
		r.provider("french", "fr").answer(nil, cand(1, "fr"))
		item := episode(1)
		r.pick(r.seed(show, nil, item)[0], statedb.Scan)
		rows := r.noResults(show, 1)
		key := keyOf(t, show, item)
		if got := rowTags(rows, key, "english"); !slices.Equal(got, []string{"en"}) {
			t.Errorf("the empty English provider booked %v, want only en, the target it was asked", got)
		}
		if got := rowTags(rows, key, "french"); len(got) != 0 {
			t.Errorf("the French provider, which answered a usable candidate, booked %v", got)
		}
	})
}

func TestPartialAnswerBooksNothing(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").
			answer(nil).
			answer(provider.Fail(provider.Transient, "page 2 failed"), cand(1, "en"))
		item := episode(1)
		inst := r.seed(show, nil, item)[0]
		key := keyOf(t, show, item)
		r.pick(inst, statedb.Item)
		before := r.noResults(show, 1)
		if len(before) != 1 {
			t.Fatalf("rows after an empty answer = %v, want one", before)
		}
		got := r.pick(inst, statedb.Item)
		if p := pickOf(t, got, "en", language.Standard); len(p.Ranked) != 1 {
			t.Errorf("a partial answer's candidates were not used: %v", releases(p))
		}
		after := r.noResults(show, 1)
		if len(after) != 1 || after[0].Count != before[0].Count || after[0].Key != key {
			t.Errorf("rows after a partial answer = %v, want %v unchanged", after, before)
		}
	})
}

func TestSkippedBooksNoEvidence(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("skips", "en").answer(&provider.Skipped{Cause: "no IMDb id"})
		r.provider("answers", "en")
		item := episode(1)
		r.pick(r.seed(show, nil, item)[0], statedb.Scan)
		rows := r.noResults(show, 1)
		key := keyOf(t, show, item)
		if got := rowTags(rows, key, "skips"); len(got) != 0 {
			t.Errorf("a skipped search booked %v", got)
		}
		if got := rowTags(rows, key, "answers"); !slices.Equal(got, []string{"en"}) {
			t.Errorf("an empty answer booked %v, want en", got)
		}
	})
}

func TestNoResultBackOffDoubles(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en")
		item := episode(1)
		inst := r.seed(show, nil, item)[0]
		var waits []time.Duration
		for range 3 {
			start := time.Now()
			r.pick(inst, statedb.Item)
			rows := r.noResults(show, 1)
			if len(rows) != 1 {
				t.Fatalf("rows = %v, want one", rows)
			}
			waits = append(waits, rows[0].Next.Sub(start.Add(time.Hour)))
		}
		want := []time.Duration{search.NoResultInitial, 2 * search.NoResultInitial, 4 * search.NoResultInitial}
		if !slices.Equal(waits, want) {
			t.Errorf("waits %v, want %v", waits, want)
		}
	})
}

func TestExpiredNoResultAsksAgain(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en")
		inst := r.seed(show, nil, episode(1))[0]
		r.pick(inst, statedb.Scan)
		r.pick(inst, statedb.Scan)
		if p.calls() != 1 {
			t.Fatalf("calls %d within the row's wait, want 1", p.calls())
		}
		time.Sleep(search.NoResultInitial)
		r.pick(inst, statedb.Scan)
		if rows := r.noResults(show, 1); p.calls() != 2 || len(rows) != 1 || rows[0].Count != 2 {
			t.Errorf("past the row's wait: %d calls, rows %v; want the provider asked again and the count at 2", p.calls(), rows)
		}
	})
}
