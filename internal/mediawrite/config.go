package mediawrite

import "github.com/cplieger/subflux/internal/timing"

// Config is the post_processing section: how Deliver cleans a subtitle
// before it renders it. ParseConfig fills its defaults.
type Config struct {
	StripHI              bool
	StripTags            bool
	NormalizeUTF8        bool
	CleanWhitespace      bool
	NormalizeLineEndings bool
	RemoveEmpty          bool
}

func (c Config) cleanOptions() timing.CleanOptions {
	return timing.CleanOptions{
		StripHI: c.StripHI, StripTags: c.StripTags, CleanWhitespace: c.CleanWhitespace, RemoveEmpty: c.RemoveEmpty,
		NormalizeUTF8: c.NormalizeUTF8, NormalizeLineEndings: c.NormalizeLineEndings,
	}
}
