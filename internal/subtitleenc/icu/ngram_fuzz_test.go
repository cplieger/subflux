package icu

import "testing"

func FuzzNGram_bounded(f *testing.F) {
	f.Add([]byte("the and the and that the and "))
	f.Add([]byte("Mąż przyjedzie jutro."))
	f.Add([]byte{0xcd, 0xe5, 0x20, 0xe7, 0xed, 0xe0, 0xfe, 0x2e})
	f.Add([]byte{0x81, 0x20, 0x81, 0x20, 0x82, 0xa0, 0x82, 0xa2})
	f.Add([]byte{0xb0, 0x30, 0x81, 0x30, 0xb0, 0x7f})
	f.Add([]byte("\x1b$B8@8l\x1b(B \x1b$Z\x0e\x0f"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		for i := range singleByte {
			r := &singleByte[i]
			for _, l := range r.langs {
				m, ok := LookupModel(r.charset, l.lang)
				if !ok {
					t.Fatalf("%s/%s: no model", r.charset, l.lang)
				}
				if c := m.Confidence(data); c < 0 || c > 98 {
					t.Fatalf("%s/%s: n-gram confidence %d outside 0..98", r.charset, l.lang, c)
				}
			}
		}
		matches := detectAll(data)
		for i, m := range matches {
			if m.Confidence < 1 || m.Confidence > 100 || m.Charset == "" {
				t.Fatalf("match %+v outside 1..100 or unnamed", m)
			}
			if i > 0 && matches[i-1].Confidence < m.Confidence {
				t.Fatalf("matches out of order: %+v before %+v", matches[i-1], m)
			}
		}
	})
}
