package scan

import (
	"context"

	"github.com/cplieger/subflux/internal/status"
)

// passJobs is the jobs one whole pass carries: its SystemOwner job ("" when
// only requests made it due) and its requesters', each ended with the pass.
type passJobs struct {
	l      *loop
	system status.JobID
	ids    []status.JobID
}

// beginJobs records the pass's SystemOwner job when anything but requests
// made it due, takes the queued whole requests, and marks every job
// Running. false when Begin failed or every request it was due for has
// ended: the pass waits for the next tick, its queue untouched.
func (l *loop) beginJobs(ctx context.Context, d due, cancel context.CancelFunc) (*passJobs, bool) {
	pj := &passJobs{l: l}
	if d.system() {
		j, _, err := l.s.status.Begin(ctx, status.Job{Kind: status.Scan, Owner: status.SystemOwner, Scope: string(l.src), Source: l.src}, cancel)
		if err != nil {
			l.s.log.Warn("scan: cannot record a whole pass's job; it waits for the next tick", "source", l.src, "error", err)
			return nil, false
		}
		pj.system = j.ID
	}
	pj.ids = l.q.startWhole(&running{cancel: cancel, live: map[status.JobID]bool{}, requested: !d.system()})
	if pj.system == "" && len(pj.ids) == 0 {
		l.q.endWhole()
		return nil, false
	}
	if pj.system != "" {
		pj.ids = append(pj.ids, pj.system)
	}
	for _, id := range pj.ids {
		l.s.status.Update(id, func(j *status.Job) { j.State = status.Running })
	}
	return pj, true
}

// progress reports done roots of total (0 when unknown) on every job still
// live.
func (pj *passJobs) progress(done, total int) {
	for _, id := range pj.ids {
		pj.l.s.status.Update(id, func(j *status.Job) { j.Done, j.Total = done, total })
	}
}

// end ends every job of the pass still live with o; cause is the failure's
// text, "" otherwise.
func (pj *passJobs) end(o status.Outcome, cause string) {
	pj.l.q.endWhole()
	for _, id := range pj.ids {
		pj.l.s.status.Update(id, func(j *status.Job) { j.State, j.Outcome, j.Cause = status.Done, o, cause })
	}
}
