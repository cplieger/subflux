// © 2016 and later: Unicode, Inc. and others.
// License & terms of use: https://www.unicode.org/copyright.html
// Copyright (C) 2005-2016, International Business Machines Corporation and others. All Rights Reserved.
// Ported from ICU4C icu4c/source/i18n/csrsbcs.cpp (the CharsetRecog_8859_*,
// windows_1251, windows_1256 and KOI8_R recognizers) at 560e6db.

package icu

// langNGrams is one language's trigram table on a shared byte map.
type langNGrams struct {
	lang   string
	ngrams [64]int32
}

// sbcs is a single-byte recognizer: its charset's byte map and the trigram
// tables of the languages ICU models in it.
type sbcs struct {
	charset string
	charMap *[256]byte
	langs   []langNGrams
}

func one(lang string, ngrams *[64]int32) []langNGrams {
	return []langNGrams{{lang: lang, ngrams: *ngrams}}
}

// singleByte keeps ICU's registration order.
var singleByte = [...]sbcs{
	{charset: "ISO-8859-1", charMap: &charMap8859_1, langs: ngrams8859_1[:]},
	{charset: "ISO-8859-2", charMap: &charMap8859_2, langs: ngrams8859_2[:]},
	{charset: "ISO-8859-5", charMap: &charMap8859_5, langs: one("ru", &ngrams8859_5Ru)},
	{charset: "ISO-8859-6", charMap: &charMap8859_6, langs: one("ar", &ngrams8859_6Ar)},
	{charset: "ISO-8859-7", charMap: &charMap8859_7, langs: one("el", &ngrams8859_7El)},
	{charset: "ISO-8859-8-I", charMap: &charMap8859_8, langs: one("he", &ngrams8859_8IHe)},
	{charset: "ISO-8859-8", charMap: &charMap8859_8, langs: one("he", &ngrams8859_8He)},
	{charset: "windows-1251", charMap: &charMapWindows1251, langs: one("ru", &ngramsWindows1251)},
	{charset: "windows-1256", charMap: &charMapWindows1256, langs: one("ar", &ngramsWindows1256)},
	{charset: "KOI8-R", charMap: &charMapKOI8R, langs: one("ru", &ngramsKOI8R)},
	{charset: "ISO-8859-9", charMap: &charMap8859_9, langs: one("tr", &ngrams8859_9Tr)},
}

// Model is one language's trigram table on its charset's byte map.
type Model struct {
	charMap *[256]byte
	ngrams  *[64]int32
}

// LookupModel finds the model ICU's recognizer for charset (ICU's name, such
// as "ISO-8859-2" or "windows-1251") holds for lang.
func LookupModel(charset, lang string) (Model, bool) {
	for i := range singleByte {
		r := &singleByte[i]
		if r.charset != charset {
			continue
		}
		for j := range r.langs {
			if r.langs[j].lang == lang {
				return Model{charMap: r.charMap, ngrams: &r.langs[j].ngrams}, true
			}
		}
	}
	return Model{}, false
}

// Confidence is ICU's NGramParser over all of text, which must be in the
// model's charset: 0 to 98.
func (m Model) Confidence(text []byte) int { return ngramConfidence(text, m.ngrams, m.charMap) }
