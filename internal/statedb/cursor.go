package statedb

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
)

// ListCursor is a page's position in one list: "" asks for the first page,
// and a returned "" means the page was the last. It is not signed: a forged
// cursor reaches only a position paging reaches.
type ListCursor string

// ErrCursorInvalid refuses a cursor that does not decode to a position of
// the list and scope it is given to.
var ErrCursorInvalid = errors.New("statedb: cursor names no position of this list")

// maxListLimit bounds every list page; api validates a limit first.
const maxListLimit = 500

// listKind is a cursor's first byte, so a cursor of one list is refused by
// every other.
type listKind byte

const (
	listRoots listKind = iota + 1
	listItems
	listUnidentified
	listHistory
)

// listScope is the list and scope a cursor must name. A cursor is the list
// kind, then the last returned key, which starts with the scope's prefix;
// valid checks that key's form, and a history key's width names its scope.
type listScope struct {
	valid  func(last []byte) bool
	prefix []byte
	list   listKind
}

func (l *listScope) cursor(last []byte) ListCursor {
	return ListCursor(base64.RawURLEncoding.EncodeToString(append([]byte{byte(l.list)}, last...)))
}

// position is the last key c returned in l, nil for the first page; a page
// resumes at that key plus 0x00, so a key since deleted still positions it.
func (l *listScope) position(c ListCursor) ([]byte, error) {
	if c == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(string(c))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCursorInvalid, err)
	}
	last, ok := bytes.CutPrefix(b, []byte{byte(l.list)})
	if !ok || !bytes.HasPrefix(last, l.prefix) || !l.valid(last) {
		return nil, ErrCursorInvalid
	}
	return last, nil
}

// resumeAt is the first key past last, a forward page's From; nil for the
// first page.
func resumeAt(last []byte) []byte {
	if last == nil {
		return nil
	}
	return append(slices.Clip(last), 0x00)
}

// page collects one list page: a scan offers each row with its key until
// full, which also sets next to resume after the last row taken.
type page[T any] struct {
	next  ListCursor
	list  *listScope
	rows  []T
	last  []byte
	limit int
}

func newPage[T any](l *listScope, limit int) *page[T] {
	return &page[T]{list: l, limit: limit, rows: make([]T, 0, min(limit, 64))}
}

// full is called once another row exists: true when the page already holds
// limit rows, so that row starts the next page.
func (p *page[T]) full() bool {
	if len(p.rows) < p.limit {
		return false
	}
	p.next = p.list.cursor(p.last)
	return true
}

func (p *page[T]) add(row T, key []byte) {
	p.rows, p.last = append(p.rows, row), key
}

func checkLimit(limit int) error {
	if limit < 1 || limit > maxListLimit {
		return fmt.Errorf("statedb: list limit %d is outside 1 to %d", limit, maxListLimit)
	}
	return nil
}
