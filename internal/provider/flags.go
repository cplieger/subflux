package provider

import "github.com/cplieger/subflux/internal/language"

// Flags is a result's flags: each one the provider sets, or that texts (its
// release name, file name or comment) say as language.ReadFlags reads them.
func Flags(flagged language.Flags, texts ...string) language.Flags {
	read := language.ReadFlags(texts...)
	return language.Flags{HI: flagged.HI || read.HI, Forced: flagged.Forced || read.Forced}
}
