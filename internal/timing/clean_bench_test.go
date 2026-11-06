package timing_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/timing"
)

// BenchmarkCleanSize: time per byte stays flat from 1 to 16 MiB.
func BenchmarkCleanSize(b *testing.B) {
	for _, mib := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("%dMiB", mib), func(b *testing.B) {
			c, err := timing.ParseCues(hiDocument(mib << 20))
			if err != nil {
				b.Fatal(err)
			}
			opts := timing.CleanOptions{StripHI: true, StripTags: true, CleanWhitespace: true, RemoveEmpty: true, NormalizeLineEndings: true}
			b.SetBytes(int64(mib << 20))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := timing.Clean(c, opts, language.Tag{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// hiDocument is an SRT of about size bytes whose events carry labels,
// annotations and tags.
func hiDocument(size int) []byte {
	const text = "<i>JOHN: [door slams] Who's there?</i>\n- (whispering) MARY: It's me. ♪ la ♪\n"
	var b strings.Builder
	for n := 1; b.Len()+300 < size; n++ {
		s := n % 3600
		b.WriteString(strconv.Itoa(n))
		fmt.Fprintf(&b, "\n00:%02d:%02d,000 --> 00:%02d:%02d,500\n", s/60, s%60, s/60, s%60)
		b.WriteString(text + text + "\n")
	}
	return []byte(b.String())
}
