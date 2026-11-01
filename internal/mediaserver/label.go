// Package mediaserver is subflux's view of the media servers (Plex) that play
// a library: how each labels the subtitles beside a video.
package mediaserver

import "github.com/cplieger/subflux/internal/language"

// Label is how one server labels one subtitle stream of a video. File is ""
// when the server names no file, as Plex does today.
type Label struct {
	File                 string
	LanguageTag, Display string
	Flags                language.Flags
}

// ServerLabels is one server's labels for a video; Err is "" when the server
// answered.
type ServerLabels struct {
	Server string
	Err    string
	Labels []Label
}
