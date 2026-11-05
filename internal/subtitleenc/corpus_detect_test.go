package subtitleenc

import "github.com/cplieger/subflux/internal/language"

// corpusAnswer runs TestDetectCorpus over Detect and Decode; an Unknown or
// undecodable answer keeps the bytes.
func corpusAnswer(data []byte, hint language.Tag) (name string, text []byte) {
	enc := Detect(data, hint)
	text, err := enc.Decode(data)
	if err != nil {
		return enc.String(), data
	}
	return enc.String(), text
}
