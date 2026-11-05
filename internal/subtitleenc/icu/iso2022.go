// © 2016 and later: Unicode, Inc. and others.
// License & terms of use: https://www.unicode.org/copyright.html
// Copyright (C) 2005-2016, International Business Machines Corporation and others. All Rights Reserved.
// Ported from ICU4C icu4c/source/i18n/csr2022.cpp (CharsetRecog_2022JP) at 560e6db.

package icu

import "bytes"

var escapes2022JP = [...][]byte{
	{0x1b, 0x24, 0x28, 0x43}, // KS X 1001:1992
	{0x1b, 0x24, 0x28, 0x44}, // JIS X 212-1990
	{0x1b, 0x24, 0x40},       // JIS C 6226-1978
	{0x1b, 0x24, 0x41},       // GB 2312-80
	{0x1b, 0x24, 0x42},       // JIS X 208-1983
	{0x1b, 0x26, 0x40},       // JIS X 208 1990, 1997
	{0x1b, 0x28, 0x42},       // ASCII
	{0x1b, 0x28, 0x48},       // JIS-Roman
	{0x1b, 0x28, 0x49},       // Half-width katakana
	{0x1b, 0x28, 0x4a},       // JIS-Roman
	{0x1b, 0x2e, 0x41},       // ISO 8859-1
	{0x1b, 0x2e, 0x46},       // ISO 8859-7
}

// ISO2022JPConfidence is CharsetRecog_2022::match_2022 over all of text:
// the share of ESC bytes starting a known sequence, less 10 per sequence and
// shift byte short of five; 0 to 100.
func ISO2022JPConfidence(text []byte) int {
	hits, misses, shifts := 0, 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] == 0x1b {
			if n := escapeAt(text[i:]); n > 0 {
				hits++
				i += n - 1
				continue
			}
			misses++
		}
		if text[i] == 0x0e || text[i] == 0x0f {
			shifts++
		}
	}
	if hits == 0 {
		return 0
	}
	quality := (100*hits - 100*misses) / (hits + misses)
	if hits+shifts < 5 {
		quality -= (5 - (hits + shifts)) * 10
	}
	return max(0, quality)
}

// escapeAt is the length of the first known sequence text starts with, or 0.
func escapeAt(text []byte) int {
	for _, seq := range escapes2022JP {
		if bytes.HasPrefix(text, seq) {
			return len(seq)
		}
	}
	return 0
}
