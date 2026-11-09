// Package sonarr is the media source over Sonarr v4 (API v3) and v5 (API
// v5), through arrapi v3. A series is a root of kind Episode; every episode
// Sonarr lists is an item placed in the series' own order, with its file's
// path and scene name when it has one; the Set keys it in TVDB's aired
// order.
package sonarr

import (
	"log/slog"
	"time"

	"github.com/cplieger/arrapi/v3"
	arrsonarr "github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

// Kind is the registry row app adds to settings.Registries.Sources.
var Kind = mediasource.AdapterKind{Type: "sonarr", Schema: arr.Schema("Sonarr"), New: newAdapter}

// Adapter answers for one Sonarr entry. It is safe for concurrent use.
type Adapter struct {
	c       *arrsonarr.Client
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
	opts := []arrsonarr.Option{arrsonarr.WithTimeout(30 * time.Second), arrsonarr.WithMaxAttempts(3)}
	if deps.HTTP != nil {
		opts = append(opts, arrsonarr.WithHTTPClient(deps.HTTP))
	}
	log := slog.Default()
	if deps.Logger != nil {
		log = deps.Logger
		opts = append(opts, arrsonarr.WithLogger(deps.Logger))
	}
	c, err := arrsonarr.New(v.URL, arrapi.APIKey(v.APIKey), opts...)
	if err != nil {
		return nil, err
	}
	return &Adapter{c: c, poster: arr.NewPoster(deps.HTTP), log: log, v: v}, nil
}
