package mediasource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
)

// Adapter answers for one configured source. New makes no request and starts
// no goroutine; every failure is a *SourceError; a zero Cursor passed to
// Changes takes the head; it is safe for concurrent use.
type Adapter interface {
	Capabilities() Capabilities
	// Items returns its yield's or unread's error unchanged and stops. It
	// calls unread for a root it lists but cannot read and goes on; a failed
	// list read fails the call.
	Items(ctx context.Context, yield func(Root, []Item) error, unread Unread) error
	Root(context.Context, NativeRef) (Root, []Item, bool, error)
	Changes(context.Context, Cursor) ([]Change, Cursor, error)
	Tags(context.Context) (map[TagRef]string, error)
	RootFolders(context.Context) ([]string, error)
	Ping(context.Context) error
	Rescan(context.Context, NativeRef) error
	Poster(context.Context, NativeRef) (io.ReadCloser, string, bool, error)
}

// Unread is Items' report of a root the walk lists but cannot read or must
// refuse. The walk goes on when it returns nil; a non-nil return ends the
// walk with that error.
type Unread func(NativeRef, *SourceError) error

// AdapterKind registers one source type.
type AdapterKind struct {
	New    func(Config, Deps) (Adapter, error)
	Type   string
	Schema formschema.Schema
}

// Config is one parsed sources entry. Index is its position in the
// section, enabled: false entries counted, so a field error about it names
// sources[Index].
type Config struct {
	Values       formschema.Values
	ID           media.SourceID
	Type         string
	PathMappings []PathMapping
	ExcludeTags  []string
	Index        int
}

// PathMapping maps a source's From prefix to the local To prefix.
type PathMapping struct{ From, To string }

// Deps is what an adapter's New may copy; a nil field takes the adapter's
// default.
type Deps struct {
	HTTP   *http.Client
	Logger *slog.Logger
}

// Set is the configured sources behind one check of every answer. It is
// safe for concurrent use.
type Set struct {
	orders OrderJoin
	byID   map[media.SourceID]*source
	log    *slog.Logger
	ids    []media.SourceID
}

type source struct {
	adapter Adapter
	labels  map[string]struct{}
	m       mapper
	id      media.SourceID
	schema  formschema.Schema
	cfg     Config
	caps    Capabilities
	tags    tagCache
	roots   rootCache
}

// NewSet builds each entry's adapter over deps, keying episodes through
// orders, refusing an unknown type, a duplicate id, a failing New or a nil
// orders: ParseConfigs ran each check, so any of them is a programming fault.
func NewSet(kinds []AdapterKind, cfgs []Config, deps Deps, orders OrderJoin) (*Set, error) {
	if orders == nil || reflect.ValueOf(orders).Kind() == reflect.Pointer && reflect.ValueOf(orders).IsNil() {
		return nil, errors.New("mediasource: NewSet needs an OrderJoin")
	}
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Set{orders: orders, byID: make(map[media.SourceID]*source, len(cfgs)), log: log}
	for _, cfg := range cfgs {
		i := slices.IndexFunc(kinds, func(k AdapterKind) bool { return k.Type == cfg.Type })
		if i < 0 {
			return nil, fmt.Errorf("mediasource: source %s: unknown type %q", cfg.ID, cfg.Type)
		}
		if _, dup := s.byID[cfg.ID]; dup {
			return nil, fmt.Errorf("mediasource: source %s is configured twice", cfg.ID)
		}
		cfg.PathMappings = slices.Clone(cfg.PathMappings)
		cfg.ExcludeTags = slices.Clone(cfg.ExcludeTags)
		ad, err := kinds[i].New(cfg, deps)
		if err != nil {
			return nil, fmt.Errorf("mediasource: source %s: %w", cfg.ID, err)
		}
		src := &source{
			id: cfg.ID, cfg: cfg, schema: kinds[i].Schema, adapter: ad,
			caps: ad.Capabilities(), m: newMapper(cfg.PathMappings), labels: labelSet(cfg.ExcludeTags),
		}
		src.caps.Kinds = slices.Clone(src.caps.Kinds)
		if !src.caps.Tags && hasConfiguredLabel(src.labels) {
			log.Warn("source cannot read tags, so its exclude_tags exclude nothing", "source", cfg.ID)
		}
		s.byID[cfg.ID] = src
		s.ids = append(s.ids, cfg.ID)
	}
	return s, nil
}

// Sources lists the configured ids in entry order.
func (s *Set) Sources() []media.SourceID { return slices.Clone(s.ids) }

// Capabilities answers a source's capabilities; false for an unknown id.
func (s *Set) Capabilities(id media.SourceID) (Capabilities, bool) {
	src, ok := s.byID[id]
	if !ok {
		return Capabilities{}, false
	}
	c := src.caps
	c.Kinds = slices.Clone(c.Kinds)
	return c, true
}

// call starts one Set call: UnknownSource for an unknown id, Unsupported
// when need is false for the source.
func (s *Set) call(id media.SourceID, op string, need func(Capabilities) bool) (*answer, error) {
	src, ok := s.byID[id]
	if !ok {
		return nil, &SourceError{Source: id, Op: op, Cause: UnknownSource}
	}
	if need != nil && !need(src.caps) {
		return nil, &SourceError{Source: id, Op: op, Cause: Unsupported}
	}
	return &answer{src: src, log: s.log, op: op}, nil
}

var errNoAnswer = fmt.Errorf("did not answer in time: %w", context.DeadlineExceeded)

// fail maps an adapter call's error: the caller's cancellation unwrapped, a
// passed deadline Unreachable unless the source answered, anything else
// through adapterError.
func fail(ctx context.Context, a *answer, err error) error {
	switch ctxErr := ctx.Err(); {
	case err == nil:
		return nil
	case errors.Is(ctxErr, context.Canceled):
		return ctxErr
	case errors.Is(ctxErr, context.DeadlineExceeded) && !answered(err):
		return &SourceError{Source: a.src.id, Op: a.op, Cause: Unreachable, Err: errNoAnswer}
	}
	return a.adapterError(err)
}

// answered reports whether err is the source's own classified answer, which
// a deadline passing after it must not replace: a rescan the source may have
// run is never sent again.
func answered(err error) bool {
	var se *SourceError
	return errors.As(err, &se) && se != nil && se.Cause != 0 && !errors.Is(err, context.DeadlineExceeded)
}

// Items walks the source's library, yielding each root after its answer
// check, exclusion and aired keys. A root breaking a per-root bound, or whose
// aired order cannot be read, goes to unread with nothing yielded, as do the
// adapter's own reports; an identity violation or a join's store failure
// fails the call. The walk ends with the error of the walkFailBudget-th
// consecutive unread root the source failed, or of the unread root past
// maxUnreadRoots. The caller's yield or unread error comes back unwrapped.
// unread must be non-nil.
func (s *Set) Items(ctx context.Context, id media.SourceID, yield func(Root, []Item) error, unread Unread) error {
	a, err := s.call(id, "Items", nil)
	if err != nil {
		return err
	}
	excluded, err := s.exclusions(ctx, a, true)
	if err != nil {
		return err
	}
	w := &walk{a: a, orders: s.orders, yield: yield, unread: unread, excluded: excluded, seen: map[NativeRef]struct{}{}}
	err = a.src.adapter.Items(ctx, func(r Root, items []Item) error { return w.root(ctx, r, items) }, w.adapterUnread)
	if w.end != nil {
		return w.end
	}
	return fail(ctx, a, err)
}

// walk is one Items call's state. end is the first error that ends it, the
// caller's own or a refusal, and wins over whatever the adapter returns.
type walk struct {
	end      error
	orders   OrderJoin
	a        *answer
	yield    func(Root, []Item) error
	unread   Unread
	excluded map[TagRef]struct{}
	seen     map[NativeRef]struct{}
	run      int
	total    int
}

func (w *walk) root(ctx context.Context, r Root, items []Item) error { //nolint:gocritic // hugeParam: an adapter yields each Root by value
	if w.end != nil {
		return w.end
	}
	if se := w.identity(r.Native); se != nil {
		return w.stop(se)
	}
	out, se := w.a.items(r.Native, items, w.excluded)
	if se != nil {
		return w.skip(r.Native, se)
	}
	root := w.a.root(r)
	se, err := w.a.key(ctx, w.orders, &root, titlesOf(items), out)
	switch {
	case err != nil:
		w.end = err
		return err
	case se != nil:
		return w.skipJoin(r.Native, se)
	}
	w.run = 0
	if err := w.yield(root, out); err != nil {
		w.end = err
		return err
	}
	return nil
}

func (w *walk) adapterUnread(ref NativeRef, se *SourceError) error {
	if w.end != nil {
		return w.end
	}
	if bad := w.identity(ref); bad != nil {
		return w.stop(bad)
	}
	if se == nil {
		return w.skip(ref, w.a.refuse("root %s reported unread with no error", ref.ID))
	}
	return w.skip(ref, w.a.adapterError(se))
}

// identity checks a root the walk reaches, yielded or unread: the id rule,
// a listed kind, and once per call.
func (w *walk) identity(ref NativeRef) *SourceError {
	if se := w.a.ref(ref); se != nil {
		return se
	}
	if _, dup := w.seen[ref]; dup {
		return w.a.refuse("root %s is listed twice", ref.ID)
	}
	w.seen[ref] = struct{}{}
	return nil
}

func (w *walk) skip(ref NativeRef, se *SourceError) error {
	w.run++
	w.total++
	if w.run >= walkFailBudget || w.total > maxUnreadRoots {
		return w.stop(se)
	}
	if err := w.unread(ref, se); err != nil {
		w.end = err
		return err
	}
	return nil
}

// skipJoin reports a root whose aired order cannot be read: the source
// answered it, so it neither counts toward nor breaks the run of
// walkFailBudget, but it counts toward maxUnreadRoots.
func (w *walk) skipJoin(ref NativeRef, se *SourceError) error {
	w.total++
	if w.total > maxUnreadRoots {
		return w.stop(se)
	}
	if err := w.unread(ref, se); err != nil {
		w.end = err
		return err
	}
	return nil
}

func (w *walk) stop(se *SourceError) error {
	w.end = se
	return se
}

// Root reads one root fresh, with its items, after the answer check and
// aired keys; false with no error when the source has no such root. A root
// whose aired order cannot be read answers that *SourceError.
func (s *Set) Root(ctx context.Context, id media.SourceID, ref NativeRef) (Root, []Item, bool, error) {
	a, err := s.call(id, "Root", nil)
	if err != nil {
		return Root{}, nil, false, err
	}
	if se := a.ref(ref); se != nil {
		return Root{}, nil, false, se
	}
	excluded, err := s.exclusions(ctx, a, false)
	if err != nil {
		return Root{}, nil, false, err
	}
	r, items, ok, err := a.src.adapter.Root(ctx, ref)
	if err != nil {
		return Root{}, nil, false, fail(ctx, a, err)
	}
	if !ok {
		if len(items) > 0 || !reflect.ValueOf(r).IsZero() {
			return Root{}, nil, false, a.refuse("root %s: answered absent with a root or items", ref.ID)
		}
		return Root{}, nil, false, nil
	}
	if r.Native != ref {
		return Root{}, nil, false, a.refuse("root %s: answered root %q", ref.ID, quote(string(r.Native.ID)))
	}
	out, se := a.items(ref, items, excluded)
	if se != nil {
		return Root{}, nil, false, se
	}
	root := a.root(r)
	se, err = a.key(ctx, s.orders, &root, titlesOf(items), out)
	switch {
	case err != nil:
		return Root{}, nil, false, err
	case se != nil:
		return Root{}, nil, false, se
	}
	return root, out, true, nil
}

// Changes answers the roots changed since cur and the cursor after them;
// each change root passes the identity rule, once per answer.
func (s *Set) Changes(ctx context.Context, id media.SourceID, cur Cursor) ([]Change, Cursor, error) {
	a, err := s.call(id, "Changes", func(c Capabilities) bool { return c.Changes })
	if err != nil {
		return nil, Cursor{}, err
	}
	changes, next, err := a.src.adapter.Changes(ctx, cur)
	if err != nil {
		return nil, Cursor{}, fail(ctx, a, err)
	}
	seen := make(map[NativeRef]struct{}, len(changes))
	for _, c := range changes {
		if se := a.ref(c.Root); se != nil {
			return nil, Cursor{}, se
		}
		if _, dup := seen[c.Root]; dup {
			return nil, Cursor{}, a.refuse("change root %s is named twice", c.Root.ID)
		}
		seen[c.Root] = struct{}{}
	}
	return slices.Clone(changes), next, nil
}

// Rescan asks the source to rescan one root.
func (s *Set) Rescan(ctx context.Context, id media.SourceID, ref NativeRef) error {
	a, err := s.call(id, "Rescan", func(c Capabilities) bool { return c.Rescan })
	if err != nil {
		return err
	}
	if se := a.ref(ref); se != nil {
		return se
	}
	return fail(ctx, a, a.src.adapter.Rescan(ctx, ref))
}

// Poster opens a root's poster with its content type; false when it has
// none. The caller closes the reader.
func (s *Set) Poster(ctx context.Context, id media.SourceID, ref NativeRef) (body io.ReadCloser, contentType string, found bool, err error) {
	a, err := s.call(id, "Poster", func(c Capabilities) bool { return c.Posters })
	if err != nil {
		return nil, "", false, err
	}
	if se := a.ref(ref); se != nil {
		return nil, "", false, se
	}
	body, ctype, ok, err := a.src.adapter.Poster(ctx, ref)
	switch {
	case err != nil:
		return nil, "", false, fail(ctx, a, err)
	case !ok && body != nil:
		_ = body.Close()
		return nil, "", false, a.refuse("root %s: answered no poster with a body", ref.ID)
	case !ok:
		return nil, "", false, nil
	case body == nil:
		return nil, "", false, a.refuse("root %s: answered a poster with no body", ref.ID)
	}
	return body, line(ctype, maxTitleBytes), true, nil
}

// Ping checks that the source answers with the configured credentials.
func (s *Set) Ping(ctx context.Context, id media.SourceID) error {
	a, err := s.call(id, "Ping", nil)
	if err != nil {
		return err
	}
	return fail(ctx, a, a.src.adapter.Ping(ctx))
}

// RootFolders answers the source's root folders, dropping with one WARN any
// that breaks the path rule; a successful answer refreshes what MediaRoots
// reports for an unmapped source.
func (s *Set) RootFolders(ctx context.Context, id media.SourceID) ([]string, error) {
	a, err := s.call(id, "RootFolders", nil)
	if err != nil {
		return nil, err
	}
	folders, err := a.src.adapter.RootFolders(ctx)
	if err != nil {
		return nil, fail(ctx, a, err)
	}
	out := make([]string, 0, len(folders))
	dropped := 0
	for _, f := range folders {
		if !validPath(f) {
			dropped++
			continue
		}
		out = append(out, f)
	}
	if dropped > 0 {
		s.log.Warn("source root folders dropped by the path rule", "source", id, "dropped", dropped)
	}
	a.src.roots.store(out)
	return slices.Clone(out), nil
}
