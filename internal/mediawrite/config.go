package mediawrite

import "github.com/cplieger/subflux/internal/timing"

// Config is the post_processing section: the options Deliver cleans a
// subtitle with before it renders it. ParseConfig fills its defaults.
type Config struct {
	Clean timing.CleanOptions
}
