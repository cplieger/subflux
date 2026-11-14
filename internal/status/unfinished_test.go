package status_test

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// newestFirst is the order the status design names for every job list:
// newest started first, ties by ID.
func newestFirst(jobs []status.Job) []status.JobID {
	sorted := slices.Clone(jobs)
	slices.SortFunc(sorted, func(a, b status.Job) int {
		if c := b.Started.Compare(a.Started); c != 0 {
			return c
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	ids := make([]status.JobID, len(sorted))
	for i, j := range sorted {
		ids[i] = j.ID
	}
	return ids
}

func pageAll(s *status.Store, limit int) (ids []status.JobID, pages int) {
	var c status.UnfinishedCursor
	for {
		jobs, next := s.Unfinished(c, limit)
		pages++
		for _, j := range jobs {
			ids = append(ids, j.ID)
		}
		if next == (status.UnfinishedCursor{}) || pages > 100 {
			return ids, pages
		}
		c = next
	}
}

// pastWindow is a store holding 100 unfinished jobs, begun in pairs that
// share a start time, then 5 ended jobs started after them; want is the
// unfinished jobs' IDs, newest started first. Call it inside a bubble.
func pastWindow(t *testing.T) (s *status.Store, want []status.JobID) {
	t.Helper()
	s = open(t, statustest.NewMemPersister())
	var unfinished []status.Job
	for i := range 100 {
		if i%2 == 0 {
			time.Sleep(time.Second)
		}
		unfinished = append(unfinished, begin(t, s, 7, nil, nil))
	}
	time.Sleep(time.Second)
	for range 5 {
		finish(s, begin(t, s, 7, nil, nil).ID, status.Succeeded)
	}
	return s, newestFirst(unfinished)
}

func TestUnfinishedPastWindow(t *testing.T) {
	t.Run("the snapshot holds 50 and counts 100", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, _ := pastWindow(t)
			snap, _ := s.Snapshot()
			if len(snap.Jobs) != 50 || snap.Unfinished != 100 {
				t.Errorf("Snapshot holds %d jobs and counts %d unfinished, want 50 and 100", len(snap.Jobs), snap.Unfinished)
			}
		})
	})
	t.Run("pages of 30 reach every unfinished job once, in order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, want := pastWindow(t)
			got, pages := pageAll(s, 30)
			if !slices.Equal(got, want) || pages != 4 {
				t.Errorf("%d pages listed %d jobs, want 4 pages listing the 100 unfinished newest started first", pages, len(got))
			}
		})
	})
	t.Run("a page reaching the last answers the zero cursor", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, _ := pastWindow(t)
			if jobs, next := s.Unfinished(status.UnfinishedCursor{}, 100); len(jobs) != 100 || next != (status.UnfinishedCursor{}) {
				t.Errorf("a page of 100 listed %d with cursor %+v, want 100 and the zero cursor", len(jobs), next)
			}
		})
	})
	t.Run("a limit below 1 lists none", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, _ := pastWindow(t)
			if jobs, next := s.Unfinished(status.UnfinishedCursor{}, 0); jobs != nil || next != (status.UnfinishedCursor{}) {
				t.Errorf("limit 0 listed %d with cursor %+v, want none and the zero cursor", len(jobs), next)
			}
		})
	})
	t.Run("a job ending between pages drops out without shifting the rest", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, want := pastWindow(t)
			first, c := s.Unfinished(status.UnfinishedCursor{}, 30)
			if len(first) != 30 {
				t.Fatalf("first page holds %d, want 30", len(first))
			}
			finish(s, want[5], status.Succeeded)
			finish(s, want[40], status.Cancelled)
			second, _ := s.Unfinished(c, 30)
			wantSecond := slices.Delete(slices.Clone(want[30:61]), 10, 11)
			got := make([]status.JobID, len(second))
			for i, j := range second {
				got[i] = j.ID
			}
			if !slices.Equal(got, wantSecond) {
				t.Errorf("second page %v, want %v", got, wantSecond)
			}
			if snap, _ := s.Snapshot(); snap.Unfinished != 98 {
				t.Errorf("Unfinished %d after two ended, want 98", snap.Unfinished)
			}
		})
	})
}
