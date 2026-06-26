package opensubtitles

import (
	"fmt"
	"testing"
)

func BenchmarkFilterSearchResults(b *testing.B) {
	makeResults := func(n int) []searchResult {
		results := make([]searchResult, n)
		for i := range results {
			results[i] = searchResult{
				Attributes: searchAttributes{
					Language: "en",
					Release:  fmt.Sprintf("Movie.2024.720p.BluRay-GROUP%d", i),
					Files:    []searchFile{{FileID: 1000 + i}},
					FeatureDetails: featureDetails{
						Title: "Test Movie",
						Year:  2024,
					},
				},
			}
		}
		return results
	}

	languages := []string{"en", "fr", "de"}

	for _, size := range []int{10, 50, 200} {
		data := makeResults(size)
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				filterSearchResults(data, languages, false)
			}
		})
	}
}
