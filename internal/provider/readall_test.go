package provider_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/provider"
)

func response(body io.Reader) *http.Response {
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+"/", http.NoBody)
	return &http.Response{Body: io.NopCloser(body), Request: req}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReadAllNeverTruncates(t *testing.T) {
	t.Run("at the limit", func(t *testing.T) {
		resp := response(strings.NewReader(strings.Repeat("a", 100)))
		defer resp.Body.Close()
		b, err := provider.ReadAll(resp, 100)
		if err != nil || len(b) != 100 {
			t.Errorf("%d bytes, %v; want 100, nil", len(b), err)
		}
	})
	t.Run("one byte past it", func(t *testing.T) {
		resp := response(strings.NewReader(strings.Repeat("a", 101)))
		defer resp.Body.Close()
		b, err := provider.ReadAll(resp, 100)
		f := failure(t, err)
		if b != nil || f.Kind != provider.Permanent || f.Cause != httpclient.CauseTooLarge || f.Host != host {
			t.Errorf("%d bytes, %+v; want none, Permanent too_large", len(b), f)
		}
	})
	t.Run("a limit above MaxBody is MaxBody", func(t *testing.T) {
		resp := response(strings.NewReader(strings.Repeat("a", provider.MaxBody+1)))
		defer resp.Body.Close()
		if _, err := provider.ReadAll(resp, 1<<40); failure(t, err).Cause != httpclient.CauseTooLarge {
			t.Error("read past MaxBody")
		}
	})
	t.Run("a read error is Transient", func(t *testing.T) {
		resp := response(failingReader{})
		defer resp.Body.Close()
		_, err := provider.ReadAll(resp, 100)
		if f := failure(t, err); f.Kind != provider.Transient || !strings.Contains(f.Msg, "unexpected EOF") {
			t.Errorf("%+v, want Transient", f)
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			t.Error("the failure exposes its unredacted cause")
		}
	})
}
