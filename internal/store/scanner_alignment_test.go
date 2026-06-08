package store

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/testsupport"
)

// TestScannerAlignment validates that the column lists declared for
// the state and backoff scanners match their Scan-argument order.
// See internal/store/authdb/scanner_alignment_test.go for the auth
// scanners and a longer rationale.
func TestScannerAlignment(t *testing.T) {
	cases := []struct {
		name           string
		columns        string
		runScan        func() (sample any, args []any, err error)
		expectedFields []string
	}{
		{
			name:    "stateScanner",
			columns: stateScanner.Columns,
			runScan: func() (any, []any, error) {
				rec := &testsupport.ScanRecorder{}
				e := &api.StateEntry{}
				if err := stateScanner.ScanInto(rec, e); err != nil {
					return nil, nil, err
				}
				return e, rec.Args, nil
			},
			expectedFields: []string{
				"ID", "MediaType", "MediaID",
				"Language", "Provider", "ReleaseName",
				"Score", "Path", "Title",
				"ImdbID", "Season", "Episode",
				"", // manual is scanned into a local int (converted to bool below)
				"MediaImported",
			},
		},
		{
			name:    "backoffScanner",
			columns: backoffScanner.Columns,
			runScan: func() (any, []any, error) {
				rec := &testsupport.ScanRecorder{}
				e := &api.BackoffEntry{}
				if err := backoffScanner.ScanInto(rec, e); err != nil {
					return nil, nil, err
				}
				return e, rec.Args, nil
			},
			expectedFields: []string{
				"MediaType", "MediaID", "Language",
				"Provider", "Failures", "LastTried", "NextRetry",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sample, args, err := tc.runScan()
			if err != nil {
				t.Fatalf("scan returned error: %v", err)
			}
			cols := testsupport.SplitColumns(tc.columns)
			if len(cols) != len(args) {
				t.Errorf("column count %d != scan arg count %d\n  columns: %v",
					len(cols), len(args), cols)
			}
			fields := make([]string, len(args))
			for i, arg := range args {
				fields[i] = testsupport.FieldNameAt(sample, arg)
			}
			if tc.expectedFields != nil && !slices.Equal(fields, tc.expectedFields) {
				t.Errorf("scan-arg field order mismatch\n  columns: %v\n  got fields: %v\n  want:        %v",
					cols, fields, tc.expectedFields)
			}
		})
	}
}
