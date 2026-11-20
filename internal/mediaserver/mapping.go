package mediaserver

import "github.com/cplieger/subflux/internal/mediasource"

// swapped turns each mapping around: From local, To the server's path.
func swapped(ms []mediasource.PathMapping) []mediasource.PathMapping {
	out := make([]mediasource.PathMapping, len(ms))
	for i, m := range ms {
		out[i] = mediasource.PathMapping{From: m.To, To: m.From}
	}
	return out
}

// serverMapper maps a local video path to cfg's server view, under the
// longest local prefix containing it; an unmatched path passes through
// unless a server prefix contains it (an *mediasource.AliasedPathError).
func serverMapper(cfg *Config) mediasource.Mapper {
	return mediasource.NewMapper(swapped(cfg.PathMappings))
}
