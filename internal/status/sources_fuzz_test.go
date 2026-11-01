package status

import (
	"strings"
	"testing"
)

func FuzzAlertSource_boundedAndInjective(f *testing.F) {
	long := strings.Repeat("p", 600)
	f.Add("a", "b")
	f.Add(long+"1", long+"2")
	f.Add("x\ny", "x y")
	f.Add("\xff", "\xfe")
	f.Add("", " ")
	f.Fuzz(func(t *testing.T, a, b string) {
		for _, c := range constructors {
			sa, sb := c.make(a), c.make(b)
			if a != b && sa == sb {
				t.Fatalf("%s: %q and %q share source %q", c.kind, a, b, sa)
			}
			for _, src := range []AlertSource{sa, sb} {
				if len(src) > MaxAlertSource || !printableLine(string(src)) {
					t.Fatalf("%s: source %q out of bound", c.kind, src)
				}
				if k, ok := AlertKind(src); !ok || k != c.kind {
					t.Fatalf("AlertKind(%q) = %q, %v; want %s", src, k, ok, c.kind)
				}
			}
		}
	})
}
