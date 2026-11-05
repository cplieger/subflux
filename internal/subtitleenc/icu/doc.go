// © 2016 and later: Unicode, Inc. and others.
// License & terms of use: https://www.unicode.org/copyright.html

// Package icu is a Go port of ICU4C's charset detector (icu4c/source/i18n
// csrsbcs.cpp, csrmbcs.cpp and csr2022.cpp at 560e6db, ICU 69.1), under the
// Unicode License V3 (THIRD_PARTY_NOTICES.md). It keeps the single-byte
// n-gram models, the multibyte recognizers and ISO-2022-JP's escape count,
// and leaves out the EBCDIC, UTF, ISO-2022-KR and ISO-2022-CN ones. Every
// confidence equals ICU's on the same bytes (TestICUPortMatchesReference).
package icu
