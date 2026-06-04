package subsync

import "testing"

func FuzzPostProcessBytes(f *testing.F) {
	f.Add([]byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n\r\n"), true, true)
	f.Add([]byte{0xEF, 0xBB, 0xBF, '1', '\n'}, true, false)
	f.Add([]byte{0xFF, 0xFE, 'h', 0, 'i', 0}, true, true)
	f.Add([]byte{}, false, false)
	f.Add([]byte("no line endings"), false, true)

	f.Fuzz(func(t *testing.T, data []byte, normEnc, normLE bool) {
		opts := PostProcessOptions{
			NormalizeEncoding:    normEnc,
			NormalizeLineEndings: normLE,
		}
		// Must not panic on any input.
		_ = PostProcessBytes(data, opts)
	})
}
