package authdb

import (
	"slices"
	"testing"

	"subflux/internal/api"
	"subflux/internal/testsupport"
)

// TestScannerAlignment validates that each tableScanner in this package
// has a Scan-argument list that matches its declared columns string.
//
// Each case asserts two properties:
//
//  1. CountMatches: column count equals scan-argument count. Catches
//     additions/removals on either side.
//  2. Fields equality (when expectedFields is set): each scan-argument
//     points to the named struct field at that position, or "" for
//     local variables (e.g. totp_secret decrypts into a local []byte).
//     Catches column reorderings without matching scan-argument
//     reorderings.
//
// To add a new scanner to this test, append a case with the columns
// string, a closure that drives scanInto against a fresh sample, and
// the expected field-name list (use "" for a local variable position).
func TestScannerAlignment(t *testing.T) {
	cases := []struct {
		name           string
		columns        string
		runScan        func() (sample any, err error)
		expectedFields []string
	}{
		{
			name:    "userScanner",
			columns: userScanner.Columns,
			runScan: func() (any, error) {
				rec := &testsupport.ScanRecorder{}
				u := &api.User{}
				if err := scanUserInto(rec, u); err != nil {
					return nil, err
				}
				return record{sample: u, args: rec.Args}, nil
			},
			expectedFields: []string{
				"ID", "Username", "Email", "DisplayName",
				"PasswordHash", "Role", "OIDCSub", "OIDCIssuer",
				"Enabled", "CreatedAt", "UpdatedAt",
			},
		},
		{
			name:    "sessionScanner",
			columns: sessionScanner.Columns,
			runScan: func() (any, error) {
				rec := &testsupport.ScanRecorder{}
				s := &api.Session{}
				if err := scanSessionInto(rec, s); err != nil {
					return nil, err
				}
				return record{sample: s, args: rec.Args}, nil
			},
		},
		{
			name:    "apiKeyScanner",
			columns: apiKeyScanner.Columns,
			runScan: func() (any, error) {
				rec := &testsupport.ScanRecorder{}
				k := &api.Key{}
				if err := scanAPIKeyInto(rec, k); err != nil {
					return nil, err
				}
				return record{sample: k, args: rec.Args}, nil
			},
		},
		{
			name:    "passkeyScanner",
			columns: passkeyScanner.Columns,
			runScan: func() (any, error) {
				rec := &testsupport.ScanRecorder{}
				p := &api.PasskeyCredential{}
				if err := scanPasskeyInto(rec, p); err != nil {
					return nil, err
				}
				return record{sample: p, args: rec.Args}, nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tc.runScan()
			if err != nil {
				t.Fatalf("scanInto returned error: %v", err)
			}
			rr := r.(record)

			cols := testsupport.SplitColumns(tc.columns)

			// 1. Count match. Catches additions/removals.
			if len(cols) != len(rr.args) {
				t.Errorf("column count %d != scan arg count %d\n  columns: %v",
					len(cols), len(rr.args), cols)
			}

			// 2. Resolve each scan arg to a field name.
			fields := make([]string, len(rr.args))
			for i, arg := range rr.args {
				fields[i] = testsupport.FieldNameAt(rr.sample, arg)
			}

			// Reorder check (when an expected list is provided). Catches
			// reorderings of the columns string without matching reorders
			// of the scanInto Scan(...) call (or vice versa).
			if tc.expectedFields != nil {
				if !slices.Equal(fields, tc.expectedFields) {
					t.Errorf("scan-arg field order mismatch\n  columns: %v\n  got fields: %v\n  want:        %v",
						cols, fields, tc.expectedFields)
				}
			} else {
				// Without an expected list, just log the discovered field
				// order for human inspection. Useful when adding a new
				// scanner: copy the logged order into expectedFields.
				t.Logf("scan-arg field order: %v", fields)
			}
		})
	}
}

// record is a tiny carrier so each case's runScan closure can return
// both the populated sample and the captured args without inventing a
// per-case struct.
type record struct {
	sample any
	args   []any
}
