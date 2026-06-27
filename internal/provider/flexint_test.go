package provider

import "testing"

// TestParseFlexInt covers each parse path: a bare JSON number, a quoted
// numeric string, an empty string (treated as zero), JSON null (a no-op
// unmarshal that leaves the value at zero), and inputs that are neither a
// number nor a numeric string (which must error).
func TestParseFlexInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		wantVal int
		wantErr bool
	}{
		{name: "bare_number", in: `42`, wantVal: 42},
		{name: "negative_number", in: `-5`, wantVal: -5},
		{name: "quoted_number", in: `"123"`, wantVal: 123},
		{name: "quoted_negative", in: `"-7"`, wantVal: -7},
		{name: "empty_string_is_zero", in: `""`, wantVal: 0},
		{name: "json_null_is_zero", in: `null`, wantVal: 0},
		{name: "non_numeric_string", in: `"abc"`, wantErr: true},
		{name: "json_object", in: `{}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseFlexInt([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseFlexInt(%s) error = nil, want non-nil", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFlexInt(%s) error = %v, want nil", tc.in, err)
			}
			if got != tc.wantVal {
				t.Errorf("ParseFlexInt(%s) = %d, want %d", tc.in, got, tc.wantVal)
			}
		})
	}
}
