package assrt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
)

type detailAnswer struct {
	Sub struct {
		Subs []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
		} `json:"subs"`
	} `json:"sub"`
}

// Fetch is 2 requests: sub/detail for the subtitle's archive link, which
// expires, then the archive over https on file0.assrt.net. It returns the
// member the Ref names, through archive.Extract, or the file itself when
// the download is no archive and is that file.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	id, name, ok := parseRef(ref.Token())
	if !ok {
		return nil, provider.Fail(provider.Permanent, "not an Assrt subtitle id and file name")
	}
	var d detailAnswer
	if err := a.call(ctx, "/sub/detail", url.Values{"id": {id}}.Encode(), &d); err != nil {
		return nil, err
	}
	if len(d.Sub.Subs) == 0 {
		return nil, provider.Fail(provider.NotFound, "Assrt lists no subtitle "+id)
	}
	sub := d.Sub.Subs[0]
	link, ok := archiveLink(sub.URL)
	if !ok {
		return nil, provider.Fail(provider.Permanent, "Assrt's download link is not on "+fileHost)
	}
	data, err := a.download(ctx, link)
	if err != nil {
		return nil, err
	}
	member, err := archive.Extract(data, func(names []string) (int, error) {
		for i, n := range names {
			if n == name {
				return i, nil
			}
		}
		return 0, fmt.Errorf("no member %q among its %d", name, len(names))
	})
	switch {
	case errors.Is(err, archive.ErrNotArchive) && sub.Filename == name:
		return data, nil
	case err != nil:
		return nil, provider.Fail(provider.Permanent, "Assrt archive: "+err.Error())
	}
	return member, nil
}

// download admits and GETs the archive at link.
func (a *adapter) download(ctx context.Context, link string) ([]byte, error) {
	if err := a.p.admit(ctx, a.token, a.quota); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := a.c.Do(req)
	err = a.spentOn(err)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, provider.Fail(provider.Permanent, "Assrt answered an empty download")
	}
	return b, nil
}

// parseRef splits a Ref token, "<id>/<file name>", into its canonical
// decimal id and its name.
func parseRef(tok string) (id, name string, ok bool) {
	id, name, ok = strings.Cut(tok, "/")
	n, err := strconv.ParseUint(id, 10, 63)
	return id, name, ok && err == nil && n > 0 && strconv.FormatUint(n, 10) == id && name != ""
}

// archiveLink is a detail's url as an https link to an archive on
// file0.assrt.net, which answers it over https though the detail says
// http (testdata/README.md).
func archiveLink(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host != fileHost || u.User != nil || !strings.HasPrefix(u.Path, "/download/") || path.Clean(u.Path) != u.Path {
		return "", false
	}
	u.Scheme = "https"
	return u.String(), true
}
