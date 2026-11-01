package language

import "testing"

func TestISO3(t *testing.T) {
	for raw, want := range map[string]string{
		"fr-CA": "fra", "pt-BR": "por", "zh-Hant": "zho", "nb": "nob", "no": "nor",
		"de": "deu", "sr-Latn": "srp", "yue": "yue", "fil": "fil",
	} {
		if got, ok := ISO3(mustParse(t, raw)); !ok || got != want {
			t.Errorf("ISO3(%s) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	french, ok := FromName("French")
	if !ok {
		t.Fatal("FromName(French) is false")
	}
	if got, ok := ISO3(french); !ok || got != "fra" {
		t.Errorf("ISO3(FromName(French)) = %q, %v; want fra", got, ok)
	}
	if got, ok := ISO3(Tag{}); ok || got != "" {
		t.Errorf("ISO3(zero) = %q, %v; want \"\", false", got, ok)
	}
}
