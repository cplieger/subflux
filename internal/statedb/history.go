package statedb

import (
	"context"
	"fmt"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
)

type scopeKind byte

const (
	wholeLog scopeKind = iota
	rootScope
	itemScope
)

// HistoryScope selects the downloads History reads; the zero value is the
// whole log.
type HistoryScope struct {
	prefix []byte
	kind   scopeKind
}

// RootHistory is every download naming an item of r.
func RootHistory(r media.RootKey) HistoryScope {
	return HistoryScope{kind: rootScope, prefix: r.Bytes()}
}

// ItemHistory is every download naming k.
func ItemHistory(k media.Key) HistoryScope {
	return HistoryScope{kind: itemScope, prefix: k.Bytes()}
}

// list is h's cursor scope: a history index key is the scope's fixed-width
// key then the inverted sequence, and the whole log's key the sequence, so
// each scope's keys have their own width.
func (h HistoryScope) list() listScope {
	n := len(h.prefix) + 8
	return listScope{list: listHistory, prefix: h.prefix, valid: func(last []byte) bool { return len(last) == n }}
}

// History returns up to limit downloads of scope, newest first, and the
// cursor of the next page, "" after the last. limit outside 1 to 500 is an
// error, and a cursor of another list or scope is ErrCursorInvalid.
func (s *Store) History(ctx context.Context, scope HistoryScope, c ListCursor, limit int) ([]Download, ListCursor, error) {
	if err := checkLimit(limit); err != nil {
		return nil, "", err
	}
	l := scope.list()
	last, err := l.position(c)
	if err != nil {
		return nil, "", err
	}
	p := newPage[Download](&l, limit)
	err = s.db.View(ctx, func(r *bolttable.ReadTx) error {
		if scope.kind == wholeLog {
			return s.logPage(r, last, p)
		}
		return s.scopedPage(r, scope, last, p)
	})
	if err != nil {
		return nil, "", err
	}
	return p.rows, p.next, nil
}

// logPage pages the downloads table backwards: a reverse page resumes at To
// set to the last key.
func (s *Store) logPage(r *bolttable.ReadTx, last []byte, p *page[Download]) error {
	return s.t.downloads.Scan(r, bolttable.Range{Reverse: true, To: last}, func(seq uint64, v *downloadRow) (bool, error) {
		if p.full() {
			return false, nil
		}
		p.add(v.download(), downloadSeqCodec.AppendKey(nil, seq))
		return true, nil
	})
}

func (s *Store) scopedPage(r *bolttable.ReadTx, scope HistoryScope, last []byte, p *page[Download]) error {
	return s.historyIndex(scope).Scan(r, bolttable.Range{Prefix: scope.prefix, From: resumeAt(last)}, func(ikey []byte, seq uint64, _ []byte) (bool, error) {
		if p.full() {
			return false, nil
		}
		v, found, err := s.t.downloads.Get(r, seq)
		if err != nil {
			return false, err
		}
		if !found {
			return false, fmt.Errorf("%w: a history entry names download %d, which is gone", bolttable.ErrCorrupt, seq)
		}
		p.add(v.download(), ikey)
		return true, nil
	})
}

func (s *Store) historyIndex(scope HistoryScope) *bolttable.Index[uint64, downloadRow] {
	if scope.kind == rootScope {
		return s.t.rootHistory
	}
	return s.t.itemHistory
}

// hasHistory is one seek: whether scope names any download.
func (s *Store) hasHistory(r *bolttable.ReadTx, scope HistoryScope) (bool, error) {
	var has bool
	err := s.historyIndex(scope).Scan(r, bolttable.Range{Prefix: scope.prefix}, func([]byte, uint64, []byte) (bool, error) {
		has = true
		return false, nil
	})
	return has, err
}
