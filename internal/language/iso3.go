package language

// ISO3 is the ISO 639-3 code of t's own language subtag (fra for fr-CA, nob
// for nb, nor for no), the one way subflux turns a tag into a three-letter
// code; false for the zero Tag and a tag langtag gives no code.
func ISO3(t Tag) (string, bool) {
	code := t.t.ISO3()
	return code, code != ""
}
