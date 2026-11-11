package search_test

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/searchtest"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// rig is an engine's world: a real store with sonarr and radarr active,
// a media root holding the videos, a running writer and scripted
// providers. Build one inside a synctest bubble with inRig.
type rig struct {
	t         *testing.T
	db        *statedb.Store
	st        *status.Store
	funnel    *providercalls.Funnel
	writer    *mediawrite.Writer
	policy    language.Policy
	providers map[provider.ProviderID]*scripted
	dir       string
	entries   []provider.Entry
	cfgs      []provider.Config
	// aligner answers every engine's Align calls as the test last set it;
	// nil answers none.
	aligner    search.Aligner
	alignerMu  sync.Mutex
	logger     *slog.Logger
	logs       *capture.Recorder
	stopWriter func()
	starts     *search.Starts
	// seeded are each series root's items as seed saw them, its default
	// reading; readings replaces one, and evidenceErr fails every read.
	seeded      map[mediasource.NativeRef][]mediasource.Item
	readings    map[evidenceKey]*mediasource.SeriesEvidence
	evidenceErr error
	asked       []evidenceKey
	evidenceMu  sync.Mutex
	dbPath      string
	cfg         search.Config
	timing      timing.Config
	roots       []string
}

// inRig runs f in a synctest bubble over a fresh rig whose languages
// section is policyYAML.
func inRig(t *testing.T, policyYAML string, f func(t *testing.T, r *rig)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, policyYAML)
		f(t, r)
	})
}

func newRig(t *testing.T, policyYAML string) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), providers: map[provider.ProviderID]*scripted{}}
	r.roots = []string{r.dir}
	r.starts = search.NewStarts()
	r.seeded = map[mediasource.NativeRef][]mediasource.Item{}
	r.readings = map[evidenceKey]*mediasource.SeriesEvidence{}
	r.dbPath = filepath.Join(t.TempDir(), "state.bolt")
	r.logger, r.logs = capture.New()
	// The writer takes an empty root for an unmounted one.
	if err := os.WriteFile(filepath.Join(r.dir, ".root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r.policy = parsePolicy(t, policyYAML)
	var err error
	if r.st, err = status.New(t.Context(), statustest.NewMemPersister()); err != nil {
		t.Fatal(err)
	}
	r.open()
	t.Cleanup(r.close)
	cfg, errs := search.ParseConfig(nil, r.policy)
	if errs != nil {
		t.Fatal(errs)
	}
	r.cfg = cfg
	return r
}

// open opens the rig's store, activates it and runs a writer and a funnel
// over it.
func (r *rig) open() {
	r.t.Helper()
	db, err := statedb.Open(r.dbPath)
	if err != nil {
		r.t.Fatal(err)
	}
	r.db = db
	if err := db.Activate(r.t.Context(), statedb.Activation{Sources: []media.SourceID{"radarr", "sonarr", "sonarr-b"}, Languages: r.policy}); err != nil {
		r.t.Fatal(err)
	}
	r.funnel = providercalls.NewFunnel(db, plainHasher{}, r.st)
	r.writer = mediawrite.New(mediawrite.Deps{Store: db, Roots: r, Alerts: r.st, Policy: db.Policy})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.writer.Run(ctx) }()
	r.stopWriter = func() {
		cancel()
		if err := <-done; err != nil {
			r.t.Errorf("writer Run: %v", err)
		}
	}
}

// close stops the writer and closes the store.
func (r *rig) close() {
	r.stopWriter()
	_ = r.db.Close()
}

// reopen restarts the rig's process state over its store and media: a new
// store handle, writer and funnel, so an engine built after it sees what a
// restart would.
func (r *rig) reopen() {
	r.t.Helper()
	r.close()
	r.open()
}

// rigAligner answers with the rig's aligner of the moment.
type rigAligner struct{ r *rig }

func (a rigAligner) Align(ctx context.Context, inst statedb.InstanceID, payload []byte, tag language.Tag, d timing.Decision) (syncsched.Aligned, error) {
	a.r.alignerMu.Lock()
	al := a.r.aligner
	a.r.alignerMu.Unlock()
	if al == nil {
		return syncsched.Aligned{}, searchtest.ErrScriptEnded
	}
	return al.Align(ctx, inst, payload, tag, d)
}

// evidenceKey is one Evidence question: a source's root.
type evidenceKey struct {
	src  media.SourceID
	root mediasource.NativeRef
}

// rigEvidence answers the rig's reading of a root: the one the test set,
// else a single-order reading of the items seed saw, titled in English.
type rigEvidence struct{ r *rig }

func (e rigEvidence) Evidence(_ context.Context, src media.SourceID, root mediasource.NativeRef) (mediasource.SeriesEvidence, bool, error) {
	r := e.r
	r.evidenceMu.Lock()
	defer r.evidenceMu.Unlock()
	k := evidenceKey{src: src, root: root}
	r.asked = append(r.asked, k)
	if r.evidenceErr != nil {
		return mediasource.SeriesEvidence{}, false, r.evidenceErr
	}
	if ev, ok := r.readings[k]; ok {
		if ev == nil {
			return mediasource.SeriesEvidence{}, false, nil
		}
		return *ev, true, nil
	}
	items, ok := r.seeded[root]
	if !ok {
		return mediasource.SeriesEvidence{}, false, nil
	}
	return airedReading(items, nil), true, nil
}

// airedReading is a reading listing items in aired order, titled in
// English, with orders' places for the same items by order type.
func airedReading(items []mediasource.Item, orders map[string][]media.Numbering) mediasource.SeriesEvidence {
	c := mediasource.Catalogue{TVDB: 1}
	titles := mediasource.TitleSet{Language: "eng", Found: true}
	for i, it := range items {
		id := uint64(1000 + i)
		c.Episodes = append(c.Episodes, mediasource.CatalogueEpisode{ID: id, Season: it.Numbering.Season, Episode: it.Numbering.Episode, Absolute: it.Absolute})
		if it.Title != "" {
			titles.Titles = append(titles.Titles, mediasource.EpisodeTitle{ID: id, Title: it.Title})
		}
	}
	for _, typ := range slices.Sorted(maps.Keys(orders)) {
		o := mediasource.CatalogueOrder{Type: typ, Name: typ}
		for _, n := range orders[typ] {
			o.Places = append(o.Places, mediasource.Place{Season: n.Season, Episode: n.Episode, Listed: true})
		}
		c.Orders = append(c.Orders, o)
	}
	return mediasource.SeriesEvidence{Catalogue: c, Titles: []mediasource.TitleSet{titles}}
}

// MediaRoots is the rig's configured roots.
func (r *rig) MediaRoots(context.Context) []string { return slices.Clone(r.roots) }

func parsePolicy(t *testing.T, text string) language.Policy {
	t.Helper()
	p, errs := language.ParsePolicy(yamlNode(t, text))
	if errs != nil {
		t.Fatal(errs)
	}
	return p
}

// show is a series root of the rig, in the aired order.
var show = mediasource.Root{
	Native: mediasource.NativeRef{ID: "73", Kind: media.Episode}, Title: "Show",
	External: media.ExternalIDs{TVDB: 121361}, Year: 2020, Order: mediasource.Order{Type: "official", Name: "Aired", Aired: true},
}

// film is a movie root of the rig.
var film = mediasource.Root{
	Native: mediasource.NativeRef{ID: "9", Kind: media.Movie}, Title: "Dune",
	External: media.ExternalIDs{TMDB: 438631}, Year: 2021,
}

// episode is show's episode S01En at its video in the rig, show's order
// being the aired one.
func episode(n int) mediasource.Item {
	name := "Show.S01E" + two(n) + ".1080p.WEB-DL.x264-GRP"
	at := media.Numbering{Scheme: media.TVDB, Season: 1, Episode: n}
	return mediasource.Item{
		Native: mediasource.NativeID("e" + two(n)), Title: "Episode " + two(n),
		VideoPath: filepath.Join("Show", name+".mkv"), SceneName: name,
		Numbering: at, SeriesNumbering: at,
	}
}

// movie is film's one item at its video in the rig.
func movie() mediasource.Item {
	name := "Dune.2021.1080p.WEB-DL.x264-GRP"
	return mediasource.Item{Native: "m1", Title: "Dune", VideoPath: filepath.Join("Dune (2021)", name+".mkv"), SceneName: name}
}

// movieCand is a candidate for film in tag, its release the movie's video
// string, matched by id.
func movieCand(tag string, edits ...func(*provider.Candidate)) provider.Candidate {
	c := provider.Candidate{
		Ref: provider.NewRef("ref-movie-" + tag), Lang: mustTag(tag),
		Release: "Dune.2021.1080p.WEB-DL.x264-GRP", MatchedBy: rank.ID,
	}
	for _, e := range edits {
		e(&c)
	}
	return c
}

func two(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }

// keyOf is item's key under root.
func keyOf(t *testing.T, root mediasource.Root, item mediasource.Item) media.Key {
	t.Helper()
	k, ok := media.Choose(root.Native.Kind, root.External, item.Numbering)
	if !ok {
		t.Fatal("no key")
	}
	return k
}

// seed has root's source see items, each at its VideoPath under the rig's
// root, writes each video and stores a probe of it with tracks, and
// returns each item's instance.
func (r *rig) seed(root mediasource.Root, tracks []statedb.EmbeddedTrack, items ...mediasource.Item) []statedb.InstanceID {
	r.t.Helper()
	src := media.SourceID("sonarr")
	if root.Native.Kind == media.Movie {
		src = "radarr"
	}
	return r.seedFrom(src, root, tracks, items...)
}

// seedFrom is seed with src the root's source.
func (r *rig) seedFrom(src media.SourceID, root mediasource.Root, tracks []statedb.EmbeddedTrack, items ...mediasource.Item) []statedb.InstanceID {
	r.t.Helper()
	ctx := r.t.Context()
	for i := range items {
		items[i].VideoPath = filepath.Join(r.dir, items[i].VideoPath)
		writeVideo(r.t, items[i].VideoPath)
	}
	if root.Native.Kind == media.Episode {
		r.evidenceMu.Lock()
		r.seeded[root.Native] = append(r.seeded[root.Native], items...)
		r.evidenceMu.Unlock()
	}
	pass, err := r.db.BeginPass(ctx, src, statedb.PassScope{})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.db.ObserveItems(ctx, src, pass, root, items); err != nil {
		r.t.Fatal(err)
	}
	var ids []statedb.InstanceID
	for _, it := range items {
		row, found, err := r.db.ItemRow(ctx, keyOf(r.t, root, it))
		if err != nil || !found || row.Inst == 0 {
			r.t.Fatalf("ItemRow = %v, %v, %v", row.Inst, found, err)
		}
		if !slices.Contains(ids, row.Inst) {
			ids = append(ids, row.Inst)
			r.storeProbe(row.Inst, it.VideoPath, tracks)
		}
	}
	return ids
}

// storeProbe stores a probe of video stamped as it is, so Probe never
// runs ffprobe.
func (r *rig) storeProbe(inst statedb.InstanceID, video string, tracks []statedb.EmbeddedTrack) {
	r.t.Helper()
	info, err := os.Stat(video)
	if err != nil {
		r.t.Fatal(err)
	}
	p := statedb.VideoProbe{Tracks: tracks, FPS: 23.976, Stamp: statedb.VideoStamp{Size: info.Size(), MtimeNs: info.ModTime().UnixNano()}}
	if err := r.db.PutEmbedded(r.t.Context(), inst, p); err != nil {
		r.t.Fatal(err)
	}
}

// writeVideo writes a 256 KiB video at path.
func writeVideo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 256<<10)
	b[0] = 1
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// provider adds an enabled scripted provider whose language map holds the
// given labels, each its own tag.
func (r *rig) provider(id provider.ProviderID, tags ...string) *scripted {
	s := &scripted{}
	var pairs []string
	for _, tag := range tags {
		pairs = append(pairs, tag, tag)
	}
	r.entries = append(r.entries, provider.Entry{
		ID: id, Summary: "A provider the test scripts.", Hosts: []string{"api.example.test"},
		Languages: language.MustProviderMap(provider.WirePairs(pairs...)),
		New:       func(provider.Client, formschema.Values) (provider.Adapter, error) { return s, nil },
	})
	r.cfgs = append(r.cfgs, provider.Config{ID: id, Enabled: true, Values: formschema.Values{}})
	r.providers[id] = s
	return s
}

// engine builds an engine over the rig as it stands.
func (r *rig) engine() *search.Engine {
	r.t.Helper()
	deps := search.Deps{
		Starts: r.starts, Store: r.db, Funnel: r.funnel, Writer: r.writer,
		Prober: embedded.New(r.db), Aligner: rigAligner{r}, Evidence: rigEvidence{r}, Status: r.st, Logger: r.logger,
	}
	e, err := search.New(deps, r.entries, r.cfgs, r.policy, r.cfg, r.timing)
	if err != nil {
		r.t.Fatal(err)
	}
	return e
}

// pick runs inst's work of cause up to the pick on a fresh engine, the
// clock moved past every provider's scan delay first.
func (r *rig) pick(inst statedb.InstanceID, cause statedb.WorkCause) []search.Picked {
	r.t.Helper()
	time.Sleep(time.Hour)
	got, err := search.PickFor(r.t.Context(), r.engine(), statedb.Work{Inst: inst, Gen: 1, Cause: cause})
	if err != nil {
		r.t.Fatalf("PickFor: %v", err)
	}
	return got
}

// noResults is every no-result row of root's season.
func (r *rig) noResults(root mediasource.Root, season uint16) []statedb.NoResult {
	r.t.Helper()
	k, ok := media.Choose(root.Native.Kind, root.External, media.Numbering{Scheme: media.TVDB, Season: int(season), Episode: 1})
	if root.Native.Kind == media.Movie {
		k, ok = media.Choose(media.Movie, root.External, media.Numbering{})
	}
	if !ok {
		r.t.Fatal("no key")
	}
	rows, err := r.db.NoResults(r.t.Context(), k.Root(), season)
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

// deliver lands a managed file name beside inst's video holding prov, as
// Deliver would leave it, its download row dated at.
func (r *rig) deliver(inst statedb.InstanceID, key media.Key, name string, tag language.Tag, flags language.Flags, prov statedb.Provenance, v language.Variant, at time.Time) {
	r.t.Helper()
	ctx := r.t.Context()
	in, _, err := r.db.Instance(ctx, inst)
	if err != nil {
		r.t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(in.VideoPath), name)
	content := "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		r.t.Fatal(err)
	}
	cues, err := timing.ParseCues([]byte(content))
	if err != nil {
		r.t.Fatal(err)
	}
	rec := statedb.FileRecord{
		Inst: inst, Name: name, Tag: tag, Flags: flags, Timing: timing.Identity{}, Provenance: &prov,
		Identity: statedb.Identity{Size: info.Size(), MtimeNs: info.ModTime().UnixNano(), SHA256: sha256.Sum256([]byte(content))},
	}
	d := statedb.PendingDeliver{Record: rec, Baseline: cues, Attempts: []statedb.Download{{
		At: at, Target: tag, Variant: v, Items: []media.Key{key}, Inst: inst, Run: statedb.RunID{1},
		Attempt: 1, Delivered: true, Candidate: [32]byte{9}, Provenance: prov,
	}}}
	if err := r.db.BeginDeliver(ctx, d); err != nil {
		r.t.Fatal(err)
	}
	if err := r.db.EndDeliver(ctx, inst, name, true); err != nil {
		r.t.Fatal(err)
	}
}

// scripted is an adapter answering each Search with the next scripted
// answer, the last one repeating, and recording every query; its Fetch
// serves each Ref's scripted payload after onFetch, when set.
type scripted struct {
	payloads map[string]scriptPayload
	onFetch  func(context.Context) error
	onSearch func(context.Context) error
	answers  []scriptAnswer
	queries  []provider.Query
	fetched  []string
	mu       sync.Mutex
}

type scriptPayload struct {
	err  error
	body []byte
}

type scriptAnswer struct {
	err   error
	cands []provider.Candidate
}

// answer scripts the next Search's answer.
func (s *scripted) answer(err error, cands ...provider.Candidate) *scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers = append(s.answers, scriptAnswer{cands: cands, err: err})
	return s
}

func (s *scripted) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) {
	s.mu.Lock()
	hook := s.onSearch
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, q)
	if len(s.answers) == 0 {
		return nil, nil
	}
	a := s.answers[0]
	if len(s.answers) > 1 {
		s.answers = s.answers[1:]
	}
	return slices.Clone(a.cands), a.err
}

// serve scripts token's Fetch: body, or err when set.
func (s *scripted) serve(token string, err error, body []byte) *scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.payloads == nil {
		s.payloads = map[string]scriptPayload{}
	}
	s.payloads[token] = scriptPayload{err: err, body: body}
	return s
}

// Fetch answers ref's scripted payload, a Permanent failure for a Ref the
// script never served.
func (s *scripted) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	s.mu.Lock()
	s.fetched = append(s.fetched, ref.Token())
	p, ok := s.payloads[ref.Token()]
	hook := s.onFetch
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return nil, err
		}
	}
	switch {
	case !ok:
		return nil, provider.Fail(provider.Permanent, "the script serves nothing for "+ref.Token())
	case p.err != nil:
		return nil, p.err
	}
	return p.body, nil
}

// fetches are the Ref tokens s was asked to fetch, in order.
func (s *scripted) fetches() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.fetched)
}

// calls is how many Searches s answered.
func (s *scripted) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queries)
}

// lastQuery is the latest query s was asked.
func (s *scripted) lastQuery() provider.Query {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries[len(s.queries)-1]
}

// cand is a candidate for show's episode n in tag, its release the
// episode's video string, matched by id.
func cand(n int, tag string, edits ...func(*provider.Candidate)) provider.Candidate {
	c := provider.Candidate{
		Ref: provider.NewRef("ref-" + tag + "-" + two(n)), Lang: mustTag(tag),
		Release: "Show.S01E" + two(n) + ".1080p.WEB-DL.x264-GRP", MatchedBy: rank.ID,
	}
	for _, e := range edits {
		e(&c)
	}
	return c
}

func mustTag(s string) language.Tag {
	tag, ok := language.Parse(s)
	if !ok {
		panic("no language " + s)
	}
	return tag
}

// releases are each ranked candidate's release name.
func releases(p search.Picked) []string {
	out := make([]string, 0, len(p.Ranked))
	for _, c := range p.Ranked {
		out = append(out, c.Release)
	}
	return out
}

// pickOf is got's pick of target and variant; it fails the test when
// there is none.
func pickOf(t *testing.T, got []search.Picked, target string, v language.Variant) search.Picked {
	t.Helper()
	for _, p := range got {
		if p.Target == mustTag(target) && p.Variant == v {
			return p
		}
	}
	t.Fatalf("no pick of %s %d in %+v", target, v, got)
	return search.Picked{}
}
