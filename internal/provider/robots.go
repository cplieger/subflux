package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cplieger/go/ssrf"
)

// The robots.txt rules for an Exception entry, RFC 9309
// (https://www.rfc-editor.org/rfc/rfc9309).
const (
	robotsProduct = "subflux"
	// robotsMaxBody is the 500 KiB RFC 9309 2.5 requires a crawler to parse;
	// the rest of a longer file is ignored.
	robotsMaxBody = 500 << 10
	robotsTTL     = 24 * time.Hour
	// robotsRetry is how long a host whose robots.txt answered 5xx or could
	// not be reached stays refused (RFC 9309 2.3.1.4 reads it as disallowing
	// every path). Its refusal is RateLimited, so the funnel pauses the call
	// until then instead of retrying into it.
	robotsRetry     = time.Hour
	robotsRedirects = 5 // RFC 9309 2.3.1.2
)

// robotsRule is one allow or disallow line, its pattern normalized.
type robotsRule struct {
	pattern string
	allow   bool
}

// robotsRules is what a robots.txt says to subflux; nil allows every path.
type robotsRules []robotsRule

// parseRobots reads body's group for subflux: every group naming the product
// token, else every "*" group, merged (RFC 9309 2.2.1).
func parseRobots(body []byte) robotsRules {
	var p robotsParser
	for line := range bytes.Lines(body) {
		if i := bytes.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if key, value, ok := bytes.Cut(line, []byte(":")); ok {
			p.line(strings.ToLower(string(bytes.TrimSpace(key))), string(bytes.TrimSpace(value)))
		}
	}
	if p.ownSeen {
		return p.own
	}
	return p.star
}

// robotsParser is parseRobots' state: the rules of the groups naming
// subflux and of the "*" groups, and which kind the current group is.
type robotsParser struct {
	own, star                       robotsRules
	ownSeen, inOwn, inStar, inRules bool
}

func (p *robotsParser) line(key, value string) {
	switch key {
	case "user-agent":
		if p.inRules {
			p.inOwn, p.inStar, p.inRules = false, false, false
		}
		switch {
		case strings.EqualFold(productToken(value), robotsProduct):
			p.inOwn, p.ownSeen = true, true
		case value == "*":
			p.inStar = true
		}
	case "allow", "disallow":
		p.inRules = true
		if value == "" {
			return
		}
		r := robotsRule{pattern: normalize(value, false), allow: key == "allow"}
		if p.inOwn {
			p.own = append(p.own, r)
		}
		if p.inStar {
			p.star = append(p.star, r)
		}
	}
}

// productToken is v's leading run of the characters RFC 9309 2.2.1 allows
// in a product token, so "subflux/1.0" names subflux.
func productToken(v string) string {
	for i := range len(v) {
		if c := v[i]; c != '-' && c != '_' && (c|0x20 < 'a' || c|0x20 > 'z') {
			return v[:i]
		}
	}
	return v
}

// allowed reports whether uri, a request's path and query, may be fetched:
// the longest matching pattern decides, allow winning a tie (RFC 9309
// 2.2.2), and /robots.txt is always allowed.
func (rs robotsRules) allowed(uri string) bool {
	path := normalize(uri, true)
	if path == "/robots.txt" {
		return true
	}
	best, allow := -1, true
	for _, r := range rs {
		if n := len(r.pattern); n >= best && robotsMatch(r.pattern, path) {
			if n > best || r.allow {
				allow = r.allow
			}
			best = n
		}
	}
	return allow
}

// robotsMatch reports whether pattern matches path from its start: "*"
// matches any run of octets and a final "$" anchors the end.
func robotsMatch(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = pattern[:len(pattern)-1]
	}
	p, s := 0, 0
	star, mark := -1, 0
	for s < len(path) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, s
			p++
		case p < len(pattern) && pattern[p] == path[s]:
			p++
			s++
		case p == len(pattern) && !anchored:
			return true
		case star >= 0:
			mark++
			s, p = mark, star+1
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// normalize percent-decodes the unreserved characters, upper-cases the hex
// of every other escape and escapes every octet outside US-ASCII, so a
// pattern and a path compare octet by octet (RFC 9309 2.2.2). A path also
// escapes "*" and "$", which only a pattern reads as special (2.2.3).
func normalize(s string, path bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			d := unhex(s[i+1])<<4 | unhex(s[i+2])
			if unreserved(d) {
				b.WriteByte(d)
			} else {
				b.WriteByte('%')
				b.WriteByte(hex[d>>4])
				b.WriteByte(hex[d&15])
			}
			i += 2
		case c >= 0x80 || path && (c == '*' || c == '$'):
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'f'
}

func unhex(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	return c | 0x20 - 'a' + 10
}

func unreserved(c byte) bool {
	return c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'z' || c == '-' || c == '.' || c == '_' || c == '~'
}

// robotsHost is one host's robots.txt verdict. turn, a one-slot channel, is
// held for every read and write of the other fields, so concurrent first
// requests to a host send one robots.txt GET.
type robotsHost struct {
	checked      time.Time // zero: to fetch
	refusedUntil time.Time
	turn         chan struct{}
	refusal      string
	rules        robotsRules
}

// robotsKey marks the context of a robots.txt GET, which the gate, the
// request cap and the call's request count skip.
type robotsKey struct{}

func (p *pinned) robots(host string) *robotsHost {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.robotsHosts[host]
	if !ok {
		h = &robotsHost{turn: make(chan struct{}, 1)}
		p.robotsHosts[host] = h
	}
	return h
}

// robotsGate refuses req before dialing when its host's robots.txt, fetched
// first when unknown or a day old, disallows its path or could not be read.
func (p *pinned) robotsGate(req *http.Request) error {
	host := req.URL.Hostname()
	h := p.robots(host)
	ctx := req.Context()
	select {
	case h.turn <- struct{}{}:
	case <-ctx.Done():
		return transportFailure(ctx, host, ctx.Err())
	}
	defer func() { <-h.turn }()
	now := time.Now()
	if now.Before(h.refusedUntil) {
		return &Failure{Kind: RateLimited, Host: host, RetryAfter: h.refusedUntil.Sub(now), Msg: h.refusal}
	}
	if h.checked.IsZero() || now.Sub(h.checked) >= robotsTTL {
		if err := p.fetchRobots(req, h); err != nil {
			return err
		}
	}
	if !h.rules.allowed(req.URL.RequestURI()) {
		return &Failure{Kind: Permanent, Host: host, Msg: "robots.txt disallows " + req.URL.EscapedPath()}
	}
	return nil
}

// fetchRobots GETs req's host's robots.txt with req's headers and records
// the verdict in h; h.turn is held. A 429 records nothing, so the GET is
// sent again once the pause it returns lifts.
func (p *pinned) fetchRobots(req *http.Request, h *robotsHost) error {
	ctx := req.Context()
	host := req.URL.Hostname()
	rreq, err := http.NewRequestWithContext(context.WithValue(ctx, robotsKey{}, true), http.MethodGet, "https://"+req.URL.Host+"/robots.txt", http.NoBody) // #nosec G704 -- req's host, which p pinned before this gate ran
	if err != nil {
		return transportFailure(ctx, host, err)
	}
	rreq.Header = req.Header.Clone()
	rreq.Header.Del("Content-Type")
	rreq.Header.Del("Content-Length")
	c := &http.Client{Transport: p, CheckRedirect: ssrf.SafeRedirectPolicy(func(_ *http.Request, via []*http.Request) error {
		if len(via) >= robotsRedirects {
			return http.ErrUseLastResponse
		}
		return nil
	})}
	resp, err := c.Do(rreq) // #nosec G704 -- p refuses every host outside the entry's Hosts before dialing
	if err != nil {
		if ctx.Err() != nil {
			return transportFailure(ctx, host, err)
		}
		return h.refuse(host, fmt.Sprintf("robots.txt could not be read: %v", err))
	}
	defer resp.Body.Close()
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		body, err := io.ReadAll(io.LimitReader(resp.Body, robotsMaxBody))
		if err != nil {
			if ctx.Err() != nil {
				return transportFailure(ctx, host, err)
			}
			return h.refuse(host, fmt.Sprintf("robots.txt could not be read: %v", err))
		}
		h.rules = parseRobots(body)
	case code == http.StatusTooManyRequests:
		return exceptionBackoff(resp, host)
	case code >= 500:
		return h.refuse(host, fmt.Sprintf("robots.txt answered %d", code))
	default:
		h.rules = nil
	}
	h.checked = time.Now()
	return nil
}

func (h *robotsHost) refuse(host, why string) error {
	h.checked, h.rules = time.Time{}, nil
	h.refusedUntil = time.Now().Add(robotsRetry)
	h.refusal = why
	return &Failure{Kind: RateLimited, Host: host, RetryAfter: robotsRetry, Msg: why}
}
