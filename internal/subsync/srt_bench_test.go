package subsync

import (
	"strings"
	"testing"
)

func BenchmarkParseSRT(b *testing.B) {
	// Build a realistic SRT with 500 cues.
	var sb strings.Builder
	for i := 1; i <= 500; i++ {
		h := i / 3600
		m := (i % 3600) / 60
		s := i % 60
		sb.WriteString(strings.Join([]string{
			string(rune('0'+byte(i/100%10))) + string(rune('0'+byte(i/10%10))) + string(rune('0'+byte(i%10))),
			formatBenchTime(h, m, s, 0) + " --> " + formatBenchTime(h, m, s+1, 0),
			"This is subtitle line number " + string(rune('0'+byte(i%10))),
			"",
		}, "\n"))
	}
	data := sb.String()

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := ParseSRT(strings.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func formatBenchTime(h, m, s, ms int) string {
	return strings.Join([]string{
		padInt(h, 2), ":", padInt(m, 2), ":", padInt(s, 2), ",", padInt(ms, 3),
	}, "")
}

func padInt(n, width int) string {
	s := ""
	for range width {
		s = string(rune('0'+byte(n%10))) + s
		n /= 10
	}
	return s
}
