package betaseries

import (
	"io"
	"testing"
)

// FuzzBetaLangToISO exercises language code conversion with arbitrary input:
// the result is always empty, "en", or "fr".
func FuzzBetaLangToISO(f *testing.F) {
	f.Add("vo")
	f.Add("vf")
	f.Add("en")
	f.Add("fr")
	f.Add("")
	f.Add("VF")
	f.Add("unknown")
	f.Add("VO\x00")

	f.Fuzz(func(t *testing.T, code string) {
		result := betaLangToISO(code)
		if result != "" && result != "en" && result != "fr" {
			t.Fatalf("betaLangToISO(%q) = %q, want \"\"|\"en\"|\"fr\"", code, result)
		}
	})
}

// FuzzClassifyBadRequest feeds arbitrary 400-response bodies to the error
// classifier. Invariants: it never returns both an error and a body, and the
// only non-error classification (code 4001, "series not found") yields the
// synthetic empty-episodes document.
func FuzzClassifyBadRequest(f *testing.F) {
	f.Add([]byte(`{"errors":[{"code":4001,"text":"Show not found"}]}`))
	f.Add([]byte(`{"errors":[{"code":1001,"text":"Invalid API key"}]}`))
	f.Add([]byte(`{"errors":[{"code":9999,"text":"Unknown"}]}`))
	f.Add([]byte(`{"errors":[]}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, body []byte) {
		rc, err := classifyBadRequest(body)
		if err != nil {
			if rc != nil {
				t.Errorf("classifyBadRequest(%q) returned both an error and a non-nil body", body)
			}
			return
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		if string(data) != `{"episodes":[]}` {
			t.Errorf("classifyBadRequest(%q) body = %q, want %q", body, data, `{"episodes":[]}`)
		}
	})
}
