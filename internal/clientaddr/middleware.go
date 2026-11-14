package clientaddr

import (
	"context"
	"net/http"

	"github.com/cplieger/access"
)

type addrKey struct{}

// Middleware takes current's resolver once per request, stores the address
// it resolves for From and reports the request to d, which must not be nil.
// Installed outermost, it lets the access log, the limiter key and the setup
// rule read one address, and a trusted_proxies Save reach the next request.
func Middleware(current func() Resolver, d *Detector) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r := current()
			res := r.resolve(req)
			d.observe(r, req, res)
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), addrKey{}, addrOf(res))))
		})
	}
}

// From returns the address Middleware stored; false outside it.
func From(ctx context.Context) (access.ClientAddr, bool) {
	a, ok := ctx.Value(addrKey{}).(access.ClientAddr)
	return a, ok
}
