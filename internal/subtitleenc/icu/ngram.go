// © 2016 and later: Unicode, Inc. and others.
// License & terms of use: https://www.unicode.org/copyright.html
// Copyright (C) 2005-2016, International Business Machines Corporation and others. All Rights Reserved.
// Ported from ICU4C icu4c/source/i18n/csrsbcs.cpp (NGramParser) at 560e6db.

package icu

import "slices"

// ngramConfidence is NGramParser.parse: the share of text's trigrams, after
// charMap folds each byte and runs of spaces collapse, found in table,
// scaled to 0..98.
func ngramConfidence(text []byte, table *[64]int32, charMap *[256]byte) int {
	var ngram int32
	hits, count := 0, 0
	add := func(b byte) {
		ngram = (ngram<<8 + int32(b)) & 0xFFFFFF
		count++
		if _, found := slices.BinarySearch(table[:], ngram); found {
			hits++
		}
	}
	ignoreSpace := false
	for _, b := range text {
		mb := charMap[b]
		if mb == 0 {
			continue
		}
		if mb != 0x20 || !ignoreSpace {
			add(mb)
		}
		ignoreSpace = mb == 0x20
	}
	add(0x20)
	raw := float64(hits) / float64(count)
	if raw > 0.33 {
		return 98
	}
	return int(raw * 300)
}
