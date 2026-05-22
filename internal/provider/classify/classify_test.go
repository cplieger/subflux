package classify

import "testing"

func TestIsForced(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		comment string
		want    bool
	}{
		{"empty", "", false},
		{"forced lowercase", "forced subtitle", true},
		{"Forced mixed case", "Forced Subtitle", true},
		{"foreign", "foreign parts only", true},
		{"Foreign mixed case", "Foreign Parts", true},
		{"normal subtitle", "English subtitle", false},
		{"hi subtitle", "hearing impaired", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsForced(tt.comment); got != tt.want {
				t.Errorf("IsForced(%q) = %v, want %v", tt.comment, got, tt.want)
			}
		})
	}
}

func TestIsHearingImpaired(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		commentary string
		filename   string
		want       bool
	}{
		{"both empty", "", "", false},
		{"filename _hi_ short-circuits", "non hi version", "movie_hi_eng.srt", true},
		{"commentary sdh", "SDH version", "", true},
		{"commentary closed caption", "closed caption", "", true},
		{"commentary _hi_ delimited", "sub_hi_version", "", true},
		{"commentary .hi. delimited", "sub.hi.eng", "", true},
		{"commentary _cc_ delimited", "sub_cc_eng", "", true},
		{"commentary .cc. delimited", "sub.cc.eng", "", true},
		{"commentary space-cc-space", "sub cc eng", "", true},
		{"commentary space-hi-space", "this is hi sub", "", true},
		{"commentary non hi", "non hi version", "", false},
		{"commentary nonhi", "nonhi version", "", false},
		{"commentary non-hi", "non-hi subs", "", false},
		{"commentary non-sdh", "non-sdh version", "", false},
		{"commentary non sdh", "non sdh version", "", false},
		{"commentary nonsdh", "nonsdh version", "", false},
		{"commentary hi remove", "HI Remove", "", false},
		{"commentary sdh remove", "sdh remove version", "", false},
		{"commentary plain text", "good quality", "", false},
		{"case-insensitive", "SDH REMOVE", "", false},
		{"empty commentary with normal filename", "", "sub.srt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsHearingImpaired(tt.commentary, tt.filename); got != tt.want {
				t.Errorf("IsHearingImpaired(%q, %q) = %v, want %v",
					tt.commentary, tt.filename, got, tt.want)
			}
		})
	}
}

func BenchmarkIsHearingImpaired(b *testing.B) {
	cases := []struct {
		commentary string
		filename   string
	}{
		{"SDH version", "movie.srt"},
		{"good quality english subtitle", "movie_eng.srt"},
		{"closed caption for hearing impaired viewers", "movie_hi_eng.srt"},
		{"", "short.srt"},
		{"non-hi non-sdh regular subtitle track", ""},
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, c := range cases {
			_ = IsHearingImpaired(c.commentary, c.filename)
		}
	}
}
