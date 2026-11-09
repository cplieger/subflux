// Package radarr is the media source over Radarr (API v3), through arrapi
// v3. A movie is a root of kind Movie holding one item, its file's path and
// scene name when it has one; both natives are the decimal movie id.
package radarr

import (
	"log/slog"
	"time"

	"github.com/cplieger/arrapi/v3"
	arrradarr "github.com/cplieger/arrapi/v3/radarr"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

// Kind is the registry row app adds to settings.Registries.Sources.
var Kind = mediasource.AdapterKind{Type: "radarr", Schema: arr.Schema("Radarr"), New: newAdapter}

// Adapter answers for one Radarr entry. It is safe for concurrent use.
type Adapter struct {
	c       *arrradarr.Client
	poster  *arr.Poster
	log     *slog.Logger
	v       arr.Values
	version arr.Version
}

var _ mediasource.Adapter = (*Adapter)(nil)

// newAdapter is Kind.New: it validates the entry and builds the client,
// making no request. A nil Deps field leaves arrapi's default.
func newAdapter(cfg mediasource.Config, deps mediasource.Deps) (mediasource.Adapter, error) { //nolint:gocritic // hugeParam: Config is the section value AdapterKind.New takes
	v, err := arr.ReadValues(cfg.Values)
	if err != nil {
		return nil, err
	}
	opts := []arrradarr.Option{arrradarr.WithTimeout(30 * time.Second), arrradarr.WithMaxAttempts(3)}
	if deps.HTTP != nil {
		opts = append(opts, arrradarr.WithHTTPClient(deps.HTTP))
	}
	log := slog.Default()
	if deps.Logger != nil {
		log = deps.Logger
		opts = append(opts, arrradarr.WithLogger(deps.Logger))
	}
	c, err := arrradarr.New(v.URL, arrapi.APIKey(v.APIKey), opts...)
	if err != nil {
		return nil, err
	}
	return &Adapter{c: c, poster: arr.NewPoster(deps.HTTP), log: log, v: v}, nil
}
