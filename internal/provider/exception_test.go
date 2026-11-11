package provider_test

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

const exceptionUA = "Mozilla/5.0 (test)"

// site is an Exception entry's host: robots answers /robots.txt (nil: 404)
// and answer every other path (nil: 200).
type site struct {
	robots http.HandlerFunc
	answer http.HandlerFunc
	seen   []*http.Request
	mu     sync.Mutex
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.seen = append(s.seen, r)
	robots, answer := s.robots, s.answer
	s.mu.Unlock()
	switch {
	case r.URL.Path != "/robots.txt":
		if answer != nil {
			answer(w, r)
		}
	case robots != nil:
		robots(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *site) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.seen {
		out = append(out, r.URL.RequestURI())
	}
	return out
}

func (s *site) set(robots, answer http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.robots, s.answer = robots, answer
}

// exceptionAdapter builds an Exception entry with an API key whose Search
// GETs each of paths on host in turn, stopping at the first failure, and
// whose Fetch GETs the token's path.
func exceptionAdapter(t *testing.T, paths ...string) provider.Adapter {
	t.Helper()
	s := &scripted{
		search: func(ctx context.Context, c provider.Client, _ provider.Query) ([]provider.Candidate, error) {
			for _, p := range paths {
				req := request(t, ctx, http.MethodGet, "https://"+host+p)
				req.Header.Set("X-Adapter", "yes")
				if _, err := send(c, req); err != nil {
					return nil, err
				}
			}
			return nil, nil
		},
		fetch: func(ctx context.Context, c provider.Client, r provider.Ref) ([]byte, error) {
			_, err := get(t, ctx, c, "https://"+host+r.Token())
			return nil, err
		},
	}
	e := entry(s, []string{host}, keyField)
	e.Exception, e.UserAgent = true, exceptionUA
	return providertest.Build(t, e, "api_key: "+key)
}

func robotsFile(body string) http.HandlerFunc { return text(body).ServeHTTP }

func statusWith(code int, retryAfter string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(code)
	}
}

func TestExceptionRules(t *testing.T) {
	t.Run("robots.txt is read once a day with the adapter's headers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := &site{robots: robotsFile("User-agent: *\nDisallow: /private\n")}
			ctx := providertest.Serve(t, t.Context(), s, host)
			a := exceptionAdapter(t, "/a")
			for range 2 {
				if _, err := a.Search(ctx, provider.Query{}); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(24 * time.Hour)
			if _, err := a.Search(ctx, provider.Query{}); err != nil {
				t.Fatal(err)
			}
			if got, want := s.paths(), []string{"/robots.txt", "/a", "/a", "/robots.txt", "/a"}; !slices.Equal(got, want) {
				t.Errorf("requests %v, want %v", got, want)
			}
			r := s.seen[0]
			if r.UserAgent() != exceptionUA || r.Header.Get("X-Adapter") != "yes" {
				t.Errorf("robots.txt asked with agent %q and X-Adapter %q, want the adapter's", r.UserAgent(), r.Header.Get("X-Adapter"))
			}
		})
	})
	t.Run("a disallowed path is refused before it is sent", func(t *testing.T) {
		for name, robots := range map[string]string{
			"for subflux":     "User-agent: other\nDisallow: /\n\nUser-agent: Subflux\nDisallow: /private\n",
			"for every agent": "User-agent: *\nDisallow: /private\n",
			"by a wildcard":   "User-agent: *\nAllow: /\nDisallow: /*/x$\n",
		} {
			t.Run(name, func(t *testing.T) {
				s := &site{robots: robotsFile(robots)}
				ctx := providertest.Serve(t, t.Context(), s, host)
				_, err := exceptionAdapter(t, "/private/x").Search(ctx, provider.Query{})
				if f := failure(t, err); f.Kind != provider.Permanent {
					t.Errorf("%+v, want Permanent", f)
				}
				if got := s.paths(); !slices.Equal(got, []string{"/robots.txt"}) {
					t.Errorf("requests %v, want robots.txt only", got)
				}
			})
		}
	})
	t.Run("subflux's own group overrides every agent's", func(t *testing.T) {
		s := &site{robots: robotsFile("User-agent: *\nDisallow: /\n\nUser-agent: subflux\nAllow: /private\n")}
		ctx := providertest.Serve(t, t.Context(), s, host)
		if _, err := exceptionAdapter(t, "/private/x").Search(ctx, provider.Query{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a robots.txt 4xx allows every path", func(t *testing.T) {
		for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone} {
			t.Run(strconv.Itoa(code), func(t *testing.T) {
				s := &site{robots: statusWith(code, "")}
				ctx := providertest.Serve(t, t.Context(), s, host)
				if _, err := exceptionAdapter(t, "/private").Search(ctx, provider.Query{}); err != nil {
					t.Fatal(err)
				}
				if got := s.paths(); !slices.Equal(got, []string{"/robots.txt", "/private"}) {
					t.Errorf("requests %v", got)
				}
			})
		}
	})
	t.Run("an unreadable robots.txt refuses the host for an hour", func(t *testing.T) {
		for name, robots := range map[string]http.HandlerFunc{
			"503":           statusWith(http.StatusServiceUnavailable, ""),
			"network error": func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) },
		} {
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s := &site{robots: robots}
					ctx := providertest.Serve(t, t.Context(), s, host)
					a := exceptionAdapter(t, "/a")
					for _, wait := range []time.Duration{0, time.Hour - time.Second} {
						time.Sleep(wait)
						_, err := a.Search(ctx, provider.Query{})
						if f := failure(t, err); f.Kind != provider.RateLimited || f.RetryAfter <= 0 || f.RetryAfter > time.Hour {
							t.Errorf("after %v: %+v, want RateLimited until the retry", wait, f)
						}
					}
					if got := s.paths(); !slices.Equal(got, []string{"/robots.txt"}) {
						t.Errorf("requests within the hour %v, want one robots.txt", got)
					}
					s.set(nil, nil)
					time.Sleep(time.Second)
					if _, err := a.Search(ctx, provider.Query{}); err != nil {
						t.Fatal(err)
					}
					if got := s.paths(); !slices.Equal(got, []string{"/robots.txt", "/robots.txt", "/a"}) {
						t.Errorf("requests %v, want robots.txt asked again after the hour", got)
					}
				})
			})
		}
	})
	t.Run("a robots.txt 429 pauses and is asked again", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := &site{robots: statusWith(http.StatusTooManyRequests, "7200")}
			ctx := providertest.Serve(t, t.Context(), s, host)
			a := exceptionAdapter(t, "/a")
			_, err := a.Search(ctx, provider.Query{})
			if f := failure(t, err); f.Kind != provider.RateLimited || f.RetryAfter != 2*time.Hour {
				t.Errorf("%+v, want RateLimited for 2h", f)
			}
			s.set(nil, nil)
			if _, err := a.Search(ctx, provider.Query{}); err != nil {
				t.Fatal(err)
			}
			if got := s.paths(); !slices.Equal(got, []string{"/robots.txt", "/robots.txt", "/a"}) {
				t.Errorf("requests %v", got)
			}
		})
	})
	t.Run("one call sends at most 4 requests", func(t *testing.T) {
		s := &site{}
		ctx := providertest.Serve(t, t.Context(), s, host)
		a := exceptionAdapter(t, "/1", "/2", "/3", "/4", "/5")
		_, err := a.Search(ctx, provider.Query{})
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("%+v, want the fifth request Permanent", f)
		}
		if _, err := a.Fetch(ctx, provider.NewRef("/f")); err != nil {
			t.Errorf("the next call: %v", err)
		}
		if got := s.paths(); !slices.Equal(got, []string{"/robots.txt", "/1", "/2", "/3", "/4", "/f"}) {
			t.Errorf("requests %v", got)
		}
	})
	t.Run("a 403, 429 or 503 backs off an hour after one request", func(t *testing.T) {
		for _, tc := range []struct {
			retryAfter string
			code       int
			want       time.Duration
		}{
			{code: http.StatusForbidden, want: time.Hour},
			{code: http.StatusTooManyRequests, want: time.Hour},
			{code: http.StatusTooManyRequests, retryAfter: "7200", want: 2 * time.Hour},
			{code: http.StatusTooManyRequests, retryAfter: "60", want: time.Hour},
			{code: http.StatusServiceUnavailable, want: time.Hour},
		} {
			t.Run(strconv.Itoa(tc.code)+" "+tc.retryAfter, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s := &site{answer: statusWith(tc.code, tc.retryAfter)}
					ctx := providertest.Serve(t, t.Context(), s, host)
					a := exceptionAdapter(t, "/a")
					_, err := a.Search(ctx, provider.Query{})
					if f := failure(t, err); f.Kind != provider.RateLimited || f.RetryAfter != tc.want || f.Status != tc.code {
						t.Errorf("search: %+v, want RateLimited for %v", f, tc.want)
					}
					_, err = a.Fetch(ctx, provider.NewRef("/f"))
					if f := failure(t, err); f.Kind != provider.RateLimited || f.RetryAfter != tc.want {
						t.Errorf("fetch: %+v, want RateLimited for %v", f, tc.want)
					}
					if got := s.paths(); !slices.Equal(got, []string{"/robots.txt", "/a", "/f"}) {
						t.Errorf("requests %v, want each sent once", got)
					}
				})
			})
		}
	})
	t.Run("a 401 keeps the answer table", func(t *testing.T) {
		s := &site{answer: statusWith(http.StatusUnauthorized, "")}
		ctx := providertest.Serve(t, t.Context(), s, host)
		_, err := exceptionAdapter(t, "/a").Search(ctx, provider.Query{})
		if f := failure(t, err); f.Kind != provider.Auth {
			t.Errorf("%+v, want Auth", f)
		}
	})
}
