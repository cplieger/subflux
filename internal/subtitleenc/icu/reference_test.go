// © 2016 and later: Unicode, Inc. and others.
// License & terms of use: https://www.unicode.org/copyright.html
// Copyright (C) 2005-2016, International Business Machines Corporation and others. All Rights Reserved.
// detectAll is ported from ICU4C icu4c/source/i18n/csdetect.cpp at 560e6db.

package icu

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Match is one recognizer's answer, as ICU's CharsetMatch reports it.
type Match struct {
	Charset    string // ICU's name: the Windows sibling's when the first 8 KiB hold a C1 byte
	Language   string // ICU's model language, "" for ISO-2022-JP
	Confidence int    // 1 to 100
}

// bufferSize is ICU's InputText buffer: the single-byte and ISO-2022
// recognizers and the C1 signal read only the input's first bufferSize
// bytes, the multibyte recognizers all of it.
const bufferSize = 8192

// windowsName is the name ICU's single-byte recognizer reports instead of its
// charset's when the buffer holds a C1 byte.
var windowsName = map[string]string{
	"ISO-8859-1": "windows-1252", "ISO-8859-2": "windows-1250", "ISO-8859-7": "windows-1253",
	"ISO-8859-8-I": "windows-1255", "ISO-8859-8": "windows-1255", "ISO-8859-9": "windows-1254",
}

var multibyteLanguage = map[string]string{
	"Shift_JIS": "ja", "GB18030": "zh", "EUC-JP": "ja", "EUC-KR": "ko", "Big5": "zh",
}

// detectAll is ICU's CharsetDetector.detectAll (csdetect.cpp) over the
// ported recognizers: every one with a positive confidence, highest first,
// ties in ICU's registration order.
func detectAll(data []byte) []Match {
	buffer := data[:min(len(data), bufferSize)]
	c1 := slices.ContainsFunc(buffer, func(b byte) bool { return 0x80 <= b && b <= 0x9F })
	var out []Match
	for i := range singleByte {
		r := &singleByte[i]
		name := r.charset
		if w, ok := windowsName[name]; ok && c1 {
			name = w
		}
		best := Match{Confidence: -1}
		for j := range r.langs {
			if c := ngramConfidence(buffer, &r.langs[j].ngrams, r.charMap); c > best.Confidence {
				best = Match{Charset: name, Language: r.langs[j].lang, Confidence: c}
			}
		}
		if best.Confidence > 0 {
			out = append(out, best)
		}
	}
	for _, r := range multibyte {
		if c := r.Confidence(data); c > 0 {
			out = append(out, Match{Charset: r.charset, Language: multibyteLanguage[r.charset], Confidence: c})
		}
	}
	if c := ISO2022JPConfidence(buffer); c > 0 {
		out = append(out, Match{Charset: "ISO-2022-JP", Confidence: c})
	}
	slices.SortStableFunc(out, func(a, b Match) int { return b.Confidence - a.Confidence })
	return out
}

// reference is testdata/reference.json: ICU4C's detectAll on each input,
// recorded once with PyICU-binary 2.7.4 (ICU 69.1). An input is corpus
// files repeated and joined, so some cross the 8 KiB buffer, or self-written
// bytes reaching a branch the corpus does not.
type reference struct {
	ICU   string `json:"icu"`
	PyICU string `json:"pyicu"`
	Cases []struct {
		Case  string `json:"case"`
		Parts []struct {
			File  string `json:"file"`
			Times int    `json:"times"`
		} `json:"parts"`
		Bytes   []byte  `json:"bytes"`
		Size    int     `json:"size"`
		Matches []Match `json:"matches"`
	} `json:"cases"`
}

// notPorted names ICU's recognizers the port leaves out.
func notPorted(charset string) bool {
	return strings.HasPrefix(charset, "UTF-") || strings.HasPrefix(charset, "IBM42") ||
		charset == "ISO-2022-KR" || charset == "ISO-2022-CN"
}

func TestICUPortMatchesReference(t *testing.T) {
	t.Parallel()
	ref := readReference(t)
	if ref.ICU != "69.1" || len(ref.Cases) == 0 {
		t.Fatalf("reference.json: ICU %q, %d cases", ref.ICU, len(ref.Cases))
	}
	for _, c := range ref.Cases {
		t.Run(c.Case, func(t *testing.T) {
			t.Parallel()
			data := c.Bytes
			for _, p := range c.Parts {
				part, err := os.ReadFile(filepath.Join("..", "testdata", "corpus", p.File+".srt"))
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, bytes.Repeat(part, p.Times)...)
			}
			if len(data) != c.Size {
				t.Fatalf("input is %d bytes, reference recorded %d", len(data), c.Size)
			}
			want := slices.DeleteFunc(slices.Clone(c.Matches), func(m Match) bool { return notPorted(m.Charset) })
			if got := detectAll(data); !slices.Equal(got, want) {
				t.Errorf("detectAll\n got %v\nwant %v", got, want)
			}
		})
	}
}

func readReference(t *testing.T) reference {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ref reference
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}
