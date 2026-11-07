package statedb

import (
	"context"
	"fmt"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// RootEvidence returns the evidence src's last observation of root carried;
// found is false when it carried none or the root has no record.
func (s *Store) RootEvidence(ctx context.Context, src media.SourceID, root mediasource.NativeRef) (mediasource.RootEvidence, bool, error) {
	var ev mediasource.RootEvidence
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		row, ok, err := s.t.rootEvidence.Get(r, rootRef{Source: src, Root: root})
		if ok {
			ev, found = row.evidence(), true
		}
		return err
	})
	if err != nil {
		return mediasource.RootEvidence{}, false, fmt.Errorf("statedb: root evidence: %w", err)
	}
	return ev, found, nil
}

// checkEvidence refuses evidence reading another series than the root
// names; the row codec refuses what Validate refuses.
func checkEvidence(root *mediasource.Root) error {
	ev := root.Evidence
	if ev == nil {
		return nil
	}
	if got := ev.Bound.Catalogue.TVDB; got != root.External.TVDB {
		return fmt.Errorf("statedb: root %q names TVDB %d, its evidence %d", root.Native.ID, root.External.TVDB, got)
	}
	return nil
}

// putEvidence stores the root's evidence, or deletes the stored row when
// the observation carries none.
func (o *observation) putEvidence(rr rootRef, ev *mediasource.RootEvidence) error {
	if ev == nil {
		_, err := o.s.t.rootEvidence.Delete(o.tx, rr)
		return err
	}
	row := evidenceRowOf(ev)
	return o.s.t.rootEvidence.Put(o.tx, rr, &row)
}

// mapped is f over in, keeping nil apart from empty so a stored reading
// reads back as it was given.
func mapped[A, B any](in []A, f func(*A) B) []B {
	if in == nil {
		return nil
	}
	out := make([]B, len(in))
	for i := range in {
		out[i] = f(&in[i])
	}
	return out
}

func evidenceRowOf(ev *mediasource.RootEvidence) evidenceRow {
	r := evidenceRow{Bound: readingRowOf(&ev.Bound)}
	if ev.Held != nil {
		held := readingRowOf(ev.Held)
		r.Held = &held
	}
	return r
}

func (r *evidenceRow) evidence() mediasource.RootEvidence {
	ev := mediasource.RootEvidence{Bound: r.Bound.reading()}
	if r.Held != nil {
		held := r.Held.reading()
		ev.Held = &held
	}
	return ev
}

func readingRowOf(s *mediasource.SeriesEvidence) readingRow {
	c := &s.Catalogue
	return readingRow{
		Titles: mapped(s.Titles, func(t *mediasource.TitleSet) titleSetRow {
			return titleSetRow{Language: t.Language, Found: t.Found, Titles: mapped(t.Titles, func(e *mediasource.EpisodeTitle) episodeTitleRow {
				return episodeTitleRow{Title: e.Title, ID: e.ID}
			})}
		}),
		Catalogue: catalogueRow{
			Fetched: c.Fetched.UTC().Round(0), TVDB: c.TVDB,
			Episodes: mapped(c.Episodes, func(e *mediasource.CatalogueEpisode) airedEpisodeRow {
				return airedEpisodeRow{AirDate: e.AirDate, ID: e.ID, Season: e.Season, Episode: e.Episode, Absolute: e.Absolute}
			}),
			Orders: mapped(c.Orders, func(o *mediasource.CatalogueOrder) orderPlacesRow {
				return orderPlacesRow{
					Type: o.Type, Name: o.Name,
					Places: mapped(o.Places, func(p *mediasource.Place) placeRow {
						return placeRow{Season: p.Season, Episode: p.Episode, Listed: p.Listed}
					}),
					Extra: mapped(o.Extra, func(x *mediasource.ExtraPlace) extraPlaceRow {
						return extraPlaceRow{AirDate: x.AirDate, Season: x.Season, Episode: x.Episode}
					}),
				}
			}),
		},
	}
}

func (r *readingRow) reading() mediasource.SeriesEvidence {
	c := &r.Catalogue
	return mediasource.SeriesEvidence{
		Titles: mapped(r.Titles, func(t *titleSetRow) mediasource.TitleSet {
			return mediasource.TitleSet{Language: t.Language, Found: t.Found, Titles: mapped(t.Titles, func(e *episodeTitleRow) mediasource.EpisodeTitle {
				return mediasource.EpisodeTitle{Title: e.Title, ID: e.ID}
			})}
		}),
		Catalogue: mediasource.Catalogue{
			Fetched: c.Fetched, TVDB: c.TVDB,
			Episodes: mapped(c.Episodes, func(e *airedEpisodeRow) mediasource.CatalogueEpisode {
				return mediasource.CatalogueEpisode{AirDate: e.AirDate, ID: e.ID, Season: e.Season, Episode: e.Episode, Absolute: e.Absolute}
			}),
			Orders: mapped(c.Orders, func(o *orderPlacesRow) mediasource.CatalogueOrder {
				return mediasource.CatalogueOrder{
					Type: o.Type, Name: o.Name,
					Places: mapped(o.Places, func(p *placeRow) mediasource.Place {
						return mediasource.Place{Season: p.Season, Episode: p.Episode, Listed: p.Listed}
					}),
					Extra: mapped(o.Extra, func(x *extraPlaceRow) mediasource.ExtraPlace {
						return mediasource.ExtraPlace{AirDate: x.AirDate, Season: x.Season, Episode: x.Episode}
					}),
				}
			}),
		},
	}
}
