// © 2016 and later: Unicode, Inc. and others.
// License & terms of use: https://www.unicode.org/copyright.html
// Copyright (C) 2005-2016, International Business Machines Corporation and others. All Rights Reserved.
// Ported from ICU4C icu4c/source/i18n/csrmbcs.cpp at 560e6db.

package icu

import (
	"math"
	"slices"
)

// char is ICU's IteratedChar after one nextChar step.
type char struct {
	value int  // one byte, or the bytes of a multibyte character
	bad   bool // the bytes break the encoding's scheme
}

// reader yields one byte at a time and -1 past the end, as ICU's
// IteratedChar.nextByte does.
type reader struct {
	data []byte
	next int
}

func (r *reader) byte() int {
	if r.next >= len(r.data) {
		r.next++
		return -1
	}
	b := r.data[r.next]
	r.next++
	return int(b)
}

func (r *reader) done() bool { return r.next >= len(r.data) }

// Multibyte is a multibyte recognizer: nextChar reads one character, and
// commonChars lists the encoding's most frequent double-byte values, sorted.
type Multibyte struct {
	nextChar    func(*reader) char
	charset     string
	commonChars []uint16
}

// LookupMultibyte finds ICU's recognizer for charset: Shift_JIS, GB18030,
// EUC-JP, EUC-KR or Big5.
func LookupMultibyte(charset string) (Multibyte, bool) {
	for _, r := range multibyte {
		if r.charset == charset {
			return r, true
		}
	}
	return Multibyte{}, false
}

// Confidence is CharsetRecog_mbcs::match_mbcs over every byte of data: 0 to 100.
func (r Multibyte) Confidence(data []byte) int {
	doubleByte, common, bad, total := 0, 0, 0, 0
	rd := &reader{data: data}
	for !rd.done() {
		ch := r.nextChar(rd)
		total++
		switch {
		case ch.bad:
			bad++
		case ch.value > 0xFF:
			doubleByte++
			// ICU searches a three- or four-byte value by its low 16 bits.
			if _, found := slices.BinarySearch(r.commonChars, uint16(ch.value&0xFFFF)); found {
				common++
			}
		}
		if bad >= 2 && bad*5 >= doubleByte {
			return 0
		}
	}
	if doubleByte <= 10 && bad == 0 {
		if doubleByte == 0 && total < 10 {
			return 0
		}
		return 10
	}
	if doubleByte < 20*bad {
		return 0
	}
	scale := 90 / math.Log(float64(doubleByte)/4)
	return max(0, min(100, int(math.Log(float64(common)+1)*scale+10)))
}

// pair joins a lead byte and the byte after it; a missing second byte
// (-1) leaves the lead alone, as ICU does.
func pair(first, second int) int {
	if second < 0 {
		return first
	}
	return first<<8 | second
}

func sjisChar(rd *reader) char {
	first := rd.byte()
	if first <= 0x7F || (first > 0xA0 && first <= 0xDF) {
		return char{value: first}
	}
	second := rd.byte()
	return char{value: pair(first, second), bad: second < 0x40 || second > 0xFE}
}

// eucChar is shared by EUC-JP and EUC-KR. A lead byte from 0x90 to 0xA0
// reads a second byte and is no error, as upstream.
func eucChar(rd *reader) char {
	first := rd.byte()
	if first <= 0x8D {
		return char{value: first}
	}
	second := rd.byte()
	ch := char{value: pair(first, second)}
	switch {
	case first >= 0xA1 && first <= 0xFE, first == 0x8E:
		ch.bad = second < 0xA1
	case first == 0x8F:
		third := rd.byte()
		ch.value = pair(ch.value, third)
		ch.bad = third < 0xA1
	}
	return ch
}

func big5Char(rd *reader) char {
	first := rd.byte()
	if first <= 0x7F || first == 0xFF {
		return char{value: first}
	}
	second := rd.byte()
	return char{value: pair(first, second), bad: second < 0x40 || second == 0x7F || second == 0xFF}
}

// gb18030Char keeps upstream's decimal 80 in the second-byte range
// (csrmbcs.cpp, CharsetRecog_gb_18030::nextChar: "secondByte >=80"), which
// accepts 0x50 to 0x7F too; matching ICU's confidences needs the same test.
func gb18030Char(rd *reader) char {
	first := rd.byte()
	if first <= 0x80 {
		return char{value: first}
	}
	second := rd.byte()
	ch := char{value: pair(first, second)}
	if first > 0xFE {
		return ch
	}
	if (second >= 0x40 && second <= 0x7E) || (second >= 80 && second <= 0xFE) {
		return ch
	}
	if second >= 0x30 && second <= 0x39 {
		if third := rd.byte(); third >= 0x81 && third <= 0xFE {
			if fourth := rd.byte(); fourth >= 0x30 && fourth <= 0x39 {
				ch.value = ch.value<<16 | third<<8 | fourth
				return ch
			}
		}
	}
	ch.bad = true
	return ch
}

// multibyte keeps ICU's registration order.
var multibyte = [...]Multibyte{
	{charset: "Shift_JIS", commonChars: commonCharsSjis[:], nextChar: sjisChar},
	{charset: "GB18030", commonChars: commonCharsGb18030[:], nextChar: gb18030Char},
	{charset: "EUC-JP", commonChars: commonCharsEucJp[:], nextChar: eucChar},
	{charset: "EUC-KR", commonChars: commonCharsEucKr[:], nextChar: eucChar},
	{charset: "Big5", commonChars: commonCharsBig5[:], nextChar: big5Char},
}
