package provider

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/language"
)

// PayloadName is the file name an answer gives its body: the
// Content-Disposition filename, else the last segment of the answered URL's
// path, as one line of at most MaxText bytes with no directory part.
func PayloadName(resp *http.Response) string {
	name := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	if name == "" && resp.Request != nil {
		name = path.Base(resp.Request.URL.Path)
	}
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	if name == "." || name == ".." {
		return ""
	}
	name, _ = textsafe.SanitizeSingleLineCapped(name, MaxText, "")
	return name
}

// WirePairs builds an adapter's language table from alternating labels and
// tags ("English", "en", ...), for language.MustProviderMap; it panics on an
// odd count or a tag language.Parse refuses, since both are typos in a
// package-level table.
func WirePairs(labelTag ...string) []language.WirePair {
	if len(labelTag)%2 != 0 {
		panic("provider: WirePairs needs label and tag pairs")
	}
	pairs := make([]language.WirePair, 0, len(labelTag)/2)
	for i := 0; i < len(labelTag); i += 2 {
		t, ok := language.Parse(labelTag[i+1])
		if !ok || t == (language.Tag{}) {
			panic(fmt.Sprintf("provider: WirePairs: %q is no language tag", labelTag[i+1]))
		}
		pairs = append(pairs, language.WirePair{Wire: labelTag[i], Tag: t})
	}
	return pairs
}
