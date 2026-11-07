package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/cplieger/go/httpclient"
)

// ReadAll reads resp's body; the caller closes it. A body past limit (at
// most MaxBody) fails as Permanent with CauseTooLarge and is never truncated;
// a read error is Transient with its cause.
func ReadAll(resp *http.Response, limit int64) ([]byte, error) {
	limit = min(limit, MaxBody)
	ctx, host := context.Background(), ""
	if resp.Request != nil {
		ctx, host = resp.Request.Context(), resp.Request.URL.Hostname()
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, transportFailure(ctx, host, err)
	}
	if int64(len(b)) > limit {
		return nil, &Failure{Kind: Permanent, Cause: httpclient.CauseTooLarge, Host: host, Msg: fmt.Sprintf("body over its %d-byte limit", limit)}
	}
	return b, nil
}
