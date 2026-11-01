// Package language is subflux's language model: canonical BCP 47 tags over
// langtag, the subtitle targets a configuration asks for, how close an offered
// tag is to a target and whether automation may take it, and the filename
// spellings subflux writes and reads.
//
// Parse is the one canonicalizer. ReadFilename is the one filename reader; its
// exception table is read by nothing else, so a spelling it accepts never
// becomes a configured target. Every function computes and returns; none logs.
package language
