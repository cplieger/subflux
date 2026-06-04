package subsync

import "testing"

func FuzzPostProcessBytes(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), true, true)
	f.Add([]byte{0xEF, 0xBB, 0xBF, 'h', 'e', 'l', 'l', 'o'}, true, false)
	f.Add([]byte{0xFF, 0xFE, 'h', 0, 'i', 0}, true, true)
	f.Add([]byte("line1\r\nline2\nline3\r"), false, true)
	f.Add([]byte{}, true, true)

	f.Fuzz(func(t *testing.T, data []byte, normEnc, normLE bool) {
		opts := PostProcessOptions{
			NormalizeEncoding:    normEnc,
			NormalizeLineEndings: normLE,
		}
		// Must not panic on arbitrary input.
		_ = PostProcessBytes(data, opts)
	})
}

func FuzzStripHI(f *testing.F) {
	f.Add("[gunshot] Hello")
	f.Add("(music playing)")
	f.Add("♪ La la la ♪")
	f.Add("JOHN: What happened?")
	f.Add("")
	f.Add("Normal subtitle text")
	f.Add("♪♪♪")

	f.Fuzz(func(t *testing.T, text string) {
		result := stripHI(text)
		_ = result // must not panic
	})
}
