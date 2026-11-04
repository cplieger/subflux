package sourcetest

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"strings"
	"sync"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Fake serves a Library as an adapter. Fail makes an operation's calls
// answer an error and FailRoot makes one root's read fail, each until
// cleared with a nil error. Its ids may be any NativeID text, decimal or
// UUID-shaped, so Set and scan tests cover both. Safe for concurrent use.
type Fake struct {
	lib      *Library
	fail     map[string]*mediasource.SourceError
	failRoot map[mediasource.NativeRef]*mediasource.SourceError
	caps     mediasource.Capabilities
	mu       sync.Mutex
}

var _ mediasource.Adapter = (*Fake)(nil)

// NewFake serves lib with caps.
func NewFake(lib *Library, caps mediasource.Capabilities) *Fake {
	return &Fake{lib: lib, caps: caps, fail: map[string]*mediasource.SourceError{}, failRoot: map[mediasource.NativeRef]*mediasource.SourceError{}}
}

// Fail makes every call of op, an Adapter method's name, answer err; a nil
// err clears it.
func (f *Fake) Fail(op string, err *mediasource.SourceError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, op)
		return
	}
	f.fail[op] = err
}

// FailRoot makes Root(ref) answer err and Items report ref to unread with
// err and go on; a nil err clears it.
func (f *Fake) FailRoot(ref mediasource.NativeRef, err *mediasource.SourceError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.failRoot, ref)
		return
	}
	f.failRoot[ref] = err
}

// FakeSchema is FakeKind's form: an optional url and a token secret.
var FakeSchema = formschema.Schema{Fields: []formschema.Field{
	{Key: "url", Label: "URL", Kind: formschema.String},
	{Key: "token", Label: "Token", Kind: formschema.Secret},
}}

// FakeKind registers f under typ, for a Set built in a test; every entry of
// that type is served by f.
func FakeKind(typ string, f *Fake) mediasource.AdapterKind {
	return mediasource.AdapterKind{
		Type:   typ,
		Schema: FakeSchema,
		New:    func(mediasource.Config, mediasource.Deps) (mediasource.Adapter, error) { return f, nil },
	}
}

// failed answers a copy of op's injected failure, or the cancelled
// context's, as an adapter would.
func (f *Fake) failed(ctx context.Context, op string) error {
	if err := ctx.Err(); err != nil {
		return &mediasource.SourceError{Cause: mediasource.Unreachable, Err: err}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if se := f.fail[op]; se != nil {
		c := *se
		return &c
	}
	return nil
}

// rootFailure answers a copy of ref's injected failure, else the
// library's ReadFails as an unreachable read.
func (f *Fake) rootFailure(r *LibRoot) *mediasource.SourceError {
	f.mu.Lock()
	defer f.mu.Unlock()
	if se := f.failRoot[r.Root.Native]; se != nil {
		c := *se
		return &c
	}
	if r.ReadFails {
		return &mediasource.SourceError{Cause: mediasource.Unreachable, Err: errRootRead}
	}
	return nil
}

var errRootRead = errors.New("the upstream failed this root's read")

// Capabilities answers the Fake's capabilities.
func (f *Fake) Capabilities() mediasource.Capabilities { return f.caps }

// Items walks the library in insertion order.
func (f *Fake) Items(ctx context.Context, yield func(mediasource.Root, []mediasource.Item) error, unread mediasource.Unread) error {
	if err := f.failed(ctx, "Items"); err != nil {
		return err
	}
	roots, _, _, _ := f.lib.Snapshot()
	for i := range roots {
		if err := ctx.Err(); err != nil {
			return &mediasource.SourceError{Cause: mediasource.Unreachable, Err: err}
		}
		r := &roots[i]
		if se := f.rootFailure(r); se != nil {
			if err := unread(r.Root.Native, se); err != nil {
				return err
			}
			continue
		}
		if err := yield(r.Root, r.Items); err != nil {
			return err
		}
	}
	return nil
}

// Root reads one root; false when the library lists none.
func (f *Fake) Root(ctx context.Context, ref mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
	if err := f.failed(ctx, "Root"); err != nil {
		return mediasource.Root{}, nil, false, err
	}
	r, ok := f.find(ref)
	if !ok {
		return mediasource.Root{}, nil, false, nil
	}
	if se := f.rootFailure(&r); se != nil {
		return mediasource.Root{}, nil, false, se
	}
	return r.Root, r.Items, true, nil
}

func (f *Fake) find(ref mediasource.NativeRef) (LibRoot, bool) {
	roots, _, _, _ := f.lib.Snapshot()
	for i := range roots {
		if roots[i].Root.Native == ref {
			return roots[i], true
		}
	}
	return LibRoot{}, false
}

// cursorVersion leads the Fake's cursor: a version byte, then the last
// event's Seq as a big-endian uint64.
const cursorVersion = 1

// Changes names each root of an event after cur once, in event order; the
// zero Cursor takes the head and an unreadable one is Expired.
func (f *Fake) Changes(ctx context.Context, cur mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error) {
	if err := f.failed(ctx, "Changes"); err != nil {
		return nil, mediasource.Cursor{}, err
	}
	_, _, _, events := f.lib.Snapshot()
	head := uint64(len(events))
	raw, _ := cur.MarshalBinary()
	if len(raw) == 0 {
		return nil, seqCursor(head), nil
	}
	if len(raw) != 9 || raw[0] != cursorVersion || binary.BigEndian.Uint64(raw[1:]) > head {
		return nil, mediasource.Cursor{}, &mediasource.SourceError{Cause: mediasource.Expired, Err: errors.New("unreadable cursor")}
	}
	var out []mediasource.Change
	seen := map[mediasource.NativeRef]bool{}
	for _, e := range events[binary.BigEndian.Uint64(raw[1:]):] {
		if !seen[e.Root] {
			seen[e.Root] = true
			out = append(out, mediasource.Change{Root: e.Root})
		}
	}
	return out, seqCursor(head), nil
}

func seqCursor(seq uint64) mediasource.Cursor {
	var c mediasource.Cursor
	_ = c.UnmarshalBinary(binary.BigEndian.AppendUint64([]byte{cursorVersion}, seq))
	return c
}

// Tags answers the library's tags.
func (f *Fake) Tags(ctx context.Context) (map[mediasource.TagRef]string, error) {
	if err := f.failed(ctx, "Tags"); err != nil {
		return nil, err
	}
	_, tags, _, _ := f.lib.Snapshot()
	return maps.Clone(tags), nil
}

// RootFolders answers the library's folders.
func (f *Fake) RootFolders(ctx context.Context) ([]string, error) {
	if err := f.failed(ctx, "RootFolders"); err != nil {
		return nil, err
	}
	_, _, folders, _ := f.lib.Snapshot()
	return folders, nil
}

// Ping answers nil unless Fail("Ping") is set.
func (f *Fake) Ping(ctx context.Context) error { return f.failed(ctx, "Ping") }

// Rescan answers nil unless Fail("Rescan") is set.
func (f *Fake) Rescan(ctx context.Context, _ mediasource.NativeRef) error {
	return f.failed(ctx, "Rescan")
}

// Poster answers "poster:<id>" as image/jpeg for a listed root, false for
// any other.
func (f *Fake) Poster(ctx context.Context, ref mediasource.NativeRef) (body io.ReadCloser, contentType string, found bool, err error) {
	if failed := f.failed(ctx, "Poster"); failed != nil {
		return nil, "", false, failed
	}
	if _, ok := f.find(ref); !ok {
		return nil, "", false, nil
	}
	return io.NopCloser(strings.NewReader("poster:" + string(ref.ID))), "image/jpeg", true, nil
}
