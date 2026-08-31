package subflux

import (
	"time"
)

// This file was interfaces_provider.go and declared four contracts. It now
// declares none, which is why it is named for what it holds: the parameter and
// result types that the provider, schema and sync surfaces pass across package
// boundaries.
//
// Where the four went, because "look in api" is the habit this refactor is
// undoing: Provider is provider.Provider, next to the FactoryFunc that returns
// it. SonarrClient and RadarrClient are server.SonarrClient and
// server.RadarrClient, unions composed by embedding the ten narrow surfaces
// their consumers declare, at the one site that fans one arr client out to all
// of them. SchemaFunc below is the last one still here, and it is a func type
// rather than an interface.

// ShowSubtitleQuery names the two values a show-level count is taken over.
// They are both free-form strings that no call site can tell apart by shape,
// so they are named rather than positional: a transposed pair would query a
// language as if it were a show and answer zero, which the caller reads as a
// legitimate "this show has almost no subtitles" and acts on by skipping the
// whole series.
type ShowSubtitleQuery struct {
	// ImdbID is the show's IMDb identifier ("tt0903747").
	ImdbID string
	// Language is the subtitle language, in subflux's internal code space.
	Language string
}

// --- Schema ---

// SchemaFunc returns the full configuration schema for the UI.
type SchemaFunc func(providers []ProviderSchema) []SchemaSection

// --- Subtitle processing ---

// SubtitleCue represents a single subtitle entry with timing.
type SubtitleCue struct {
	Text  string
	Start time.Duration
	End   time.Duration
}
