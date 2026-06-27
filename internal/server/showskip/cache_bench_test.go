package showskip

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkCache_Lookup(b *testing.B) {
	c := New(time.Minute)
	for i := range 100 {
		c.Set(fmt.Sprintf("key-%d", i), i%2 == 0)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.Get("key-50")
	}
}
