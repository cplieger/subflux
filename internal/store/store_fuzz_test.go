package store

import (
	"strings"
	"testing"
)

// FuzzPlaceholdersInvariant verifies the partition invariant of placeholders:
// - len(args) == number of input values
// - clause contains exactly len(values) '?' characters
// - clause contains exactly max(0, len(values)-1) ',' characters
func FuzzPlaceholdersInvariant(f *testing.F) {
	f.Add("a,b,c")
	f.Add("")
	f.Add("single")
	f.Add("x,y")

	f.Fuzz(func(t *testing.T, csv string) {
		var values []string
		if csv != "" {
			values = strings.Split(csv, ",")
		}

		// Convert to the generic type expected by placeholders.
		typed := make([]providerStr, len(values))
		for i, v := range values {
			typed[i] = providerStr(v)
		}

		clause, args := placeholders(typed)

		if len(args) != len(typed) {
			t.Fatalf("args length %d != values length %d", len(args), len(typed))
		}

		qCount := strings.Count(clause, "?")
		if qCount != len(typed) {
			t.Fatalf("clause has %d '?' but expected %d", qCount, len(typed))
		}

		expectedCommas := 0
		if len(typed) > 1 {
			expectedCommas = len(typed) - 1
		}
		commaCount := strings.Count(clause, ",")
		if commaCount != expectedCommas {
			t.Fatalf("clause has %d ',' but expected %d", commaCount, expectedCommas)
		}
	})
}

// providerStr is a string type satisfying ~string for placeholders.
type providerStr string

// FuzzBatchSlicePartition verifies that batchSlice is a partition:
// concatenating all batches yields the original slice.
func FuzzBatchSlicePartition(f *testing.F) {
	f.Add("a,b,c,d,e", 2)
	f.Add("x", 1)
	f.Add("", 5)
	f.Add("a,b,c", 100)

	f.Fuzz(func(t *testing.T, csv string, batchSize int) {
		if batchSize <= 0 {
			return // invalid input, skip
		}

		var values []string
		if csv != "" {
			values = strings.Split(csv, ",")
		}

		var reconstructed []string
		for batch := range batchSlice(values, batchSize) {
			if len(batch) == 0 {
				t.Fatal("empty batch yielded")
			}
			if len(batch) > batchSize {
				t.Fatalf("batch size %d exceeds max %d", len(batch), batchSize)
			}
			reconstructed = append(reconstructed, batch...)
		}

		if len(reconstructed) != len(values) {
			t.Fatalf("reconstructed length %d != original %d", len(reconstructed), len(values))
		}
		for i := range values {
			if reconstructed[i] != values[i] {
				t.Fatalf("mismatch at index %d: %q != %q", i, reconstructed[i], values[i])
			}
		}
	})
}
