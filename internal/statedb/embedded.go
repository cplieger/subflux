package statedb

import (
	"context"
	"fmt"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
)

// VideoStamp is the video file's size and modification time when probed;
// zero for a probe that failed.
type VideoStamp struct{ Size, MtimeNs int64 }

// EmbeddedTrack is one subtitle stream inside a video file.
type EmbeddedTrack struct {
	Codec, Title string
	Tag          language.Tag
	Index        int
	Flags        language.Flags
}

// VideoProbe is what probing a video file found. A failed probe is stored
// as one with no tracks and a zero stamp, so the instance's inventory still
// completes.
type VideoProbe struct {
	Tracks []EmbeddedTrack
	Stamp  VideoStamp
	FPS    float64
}

// PutEmbedded stores inst's probe and recomputes its items' coverage in
// the same write; it deletes only locks on pairs no longer configured, since
// it runs before the local-file listing. An unknown instance is refused,
// writing nothing.
func (s *Store) PutEmbedded(ctx context.Context, inst InstanceID, p VideoProbe) error {
	row := embeddedRow{Size: p.Stamp.Size, MtimeNs: p.Stamp.MtimeNs, FPS: p.FPS, Tracks: make([]trackRow, 0, len(p.Tracks))}
	for _, t := range p.Tracks {
		row.Tracks = append(row.Tracks, trackRow{Codec: t.Codec, Title: t.Title, Tag: rowTag{t: t.Tag}, Index: t.Index, Flags: flagsByte(t.Flags)})
	}
	return s.putInventory(ctx, "put embedded", inst, false, func(tx *bolttable.Tx) error {
		return s.t.embedded.Put(tx, inst, &row)
	})
}

// Embedded returns inst's stored probe; false when none is stored.
func (s *Store) Embedded(ctx context.Context, inst InstanceID) (VideoProbe, bool, error) {
	var row embeddedRow
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		row, found, err = s.t.embedded.Get(r, inst)
		return err
	})
	if err != nil || !found {
		return VideoProbe{}, false, err
	}
	p := VideoProbe{Stamp: VideoStamp{Size: row.Size, MtimeNs: row.MtimeNs}, FPS: row.FPS, Tracks: make([]EmbeddedTrack, 0, len(row.Tracks))}
	for _, t := range row.Tracks {
		p.Tracks = append(p.Tracks, EmbeddedTrack{Codec: t.Codec, Title: t.Title, Tag: t.Tag.t, Index: t.Index, Flags: flagsOf(t.Flags)})
	}
	return p, true, nil
}

// putInventory runs put and inst's coverage recompute in one write, under
// the activation in force; inventory is whether the recompute may delete
// unservable locks.
func (s *Store) putInventory(ctx context.Context, op string, inst InstanceID, inventory bool, put func(*bolttable.Tx) error) error {
	s.actMu.RLock()
	defer s.actMu.RUnlock()
	a := s.active.Load()
	if a == nil {
		return errNoActivation
	}
	return s.write(ctx, op, s.db.Update, func(tx *bolttable.Tx) error {
		_, found, err := s.t.instances.Get(tx.Read(), inst)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("statedb: %s: instance %d is unknown", op, inst)
		}
		if err := put(tx); err != nil {
			return err
		}
		return s.recomputeInstance(tx, a, inst, inventory)
	})
}
