package release

import (
	"strings"
	"testing"
)

// Adversarial scaling benchmarks for the layer's measured linear-time gate
// (spec subflux-release-parse-fidelity R3.7): input lengths double per
// sub-benchmark against quantifier+assertion pattern shapes; near-linear
// ns/op growth (factor ~2 per doubling) within the MaxNameLen bound is the
// expected profile. Compare with benchstat across sizes:
//
//	go test ./internal/search/release/ -run '^$' -bench BenchmarkPCREScaling -benchtime 1x -count 10
//
// Sizes extend past MaxNameLen (512) by one doubling to characterize
// behavior at and beyond the enforced provider-boundary clamp.

// scalingSizes double per step; 1024 exceeds MaxNameLen deliberately.
var scalingSizes = []int{64, 128, 256, 512, 1024}

// benchPatterns are the adversarial quantifier+assertion shapes: the
// flagship release-group pattern (quantifier-adjacent lookahead+lookbehind
// with shrink-retry), the class-(h) witness (nested-branch marker), and a
// worst-case retry shape whose assertion fails at every shrink offset.
var benchPatterns = []struct {
	name    string
	pattern string
	input   func(n int) string
}{
	{
		name:    "flagship_release_group",
		pattern: sonarrReleaseGroupRegex,
		// Many hyphen-separated candidate groups; lookbehind suffixes force
		// retries; no bracket tail.
		input: func(n int) string {
			return truncTo(n, strings.Repeat("group-es.", 1+n/9))
		},
	},
	{
		name:    "witness_nested_branch",
		pattern: `\b(?:BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))\b`,
		// Alternating BD tokens: every candidate evaluates the (?!$) marker.
		input: func(n int) string {
			return truncTo(n, strings.Repeat("BD.", 1+n/3))
		},
	},
	{
		name:    "retry_worst_case",
		pattern: `(\d+)(?!x)`,
		// One maximal digit run ending in x: the shrink loop probes every
		// offset of the run and fails at each (full bounded retry cost).
		input: func(n int) string {
			return strings.Repeat("9", n-1) + "x"
		},
	},
	{
		name:    "assertion_scan_cost",
		pattern: `x(?=.*999end)`,
		// Lookahead scans the whole remaining input per candidate.
		input: func(n int) string {
			return truncTo(n, strings.Repeat("x.", n/2))
		},
	},
}

// truncTo trims a generated input to exactly n bytes.
func truncTo(n int, s string) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func BenchmarkPCREScaling(b *testing.B) {
	for _, bp := range benchPatterns {
		p, err := CompilePCRE(bp.pattern)
		if err != nil {
			b.Fatalf("CompilePCRE(%q): %v", bp.pattern, err)
		}
		for _, size := range scalingSizes {
			input := bp.input(size)
			b.Run(bp.name+"/n="+itoa(size), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					p.MatchString(input)
					p.FindStringSubmatch(input)
				}
			})
		}
	}
}

// itoa avoids strconv in the hot benchmark naming path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
