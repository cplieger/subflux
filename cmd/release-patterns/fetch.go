package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cplieger/runesafe/v3"
)

const (
	defaultRawBase = "https://raw.githubusercontent.com"
	defaultAPIBase = "https://api.github.com"
	maxFileBytes   = 8 << 20
)

var errNotFound = errors.New("not found")

// upstream reads files at a commit, each at most once per run.
type upstream struct {
	client  *http.Client
	cache   map[string][]byte
	rawBase string
	apiBase string
}

// newUpstream reads raw files from rawBase and the REST API at apiBase. A
// non-empty token authenticates the API calls only.
func newUpstream(rawBase, apiBase, token string) *upstream {
	apiBase = strings.TrimSuffix(apiBase, "/")
	client := &http.Client{Timeout: 60 * time.Second}
	if token != "" {
		client.Transport = &apiAuth{base: http.DefaultTransport, prefix: apiBase + "/", token: token}
	}
	return &upstream{
		client:  client,
		rawBase: strings.TrimSuffix(rawBase, "/"),
		apiBase: apiBase,
		cache:   map[string][]byte{},
	}
}

// apiAuth adds the token to requests under prefix only, so raw file reads
// never carry it.
type apiAuth struct {
	base          http.RoundTripper
	prefix, token string
}

func (a *apiAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(req.URL.String(), a.prefix) {
		return a.base.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+a.token)
	return a.base.RoundTrip(req)
}

func (u *upstream) file(ctx context.Context, repo, ref, path string) ([]byte, error) {
	return u.cached(ctx, u.rawBase+"/"+repo+"/"+ref+"/"+path, "")
}

// dirEntry is one file of a directory listing; SHA is its blob hash.
type dirEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

// maxDirEntries is where the contents API stops listing a directory.
const maxDirEntries = 1000

// entries lists the files directly under path.
func (u *upstream) entries(ctx context.Context, repo, ref, path string) ([]dirEntry, error) {
	link := u.apiBase + "/repos/" + repo + "/contents/" + path + "?ref=" + url.QueryEscape(ref)
	b, err := u.cached(ctx, link, "")
	if err != nil {
		return nil, err
	}
	var all []dirEntry
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("list %s: %w", path, err)
	}
	if len(all) >= maxDirEntries {
		return nil, fmt.Errorf("list %s: %d entries, the contents API's limit, so the listing may be cut", path, len(all))
	}
	var files []dirEntry
	for _, e := range all {
		if e.Type == "file" {
			files = append(files, e)
		}
	}
	return files, nil
}

// dir lists the files directly under path, by repository path.
func (u *upstream) dir(ctx context.Context, repo, ref, path string) ([]string, error) {
	entries, err := u.entries(ctx, repo, ref, path)
	if err != nil {
		return nil, err
	}
	files := make([]string, len(entries))
	for i, e := range entries {
		files[i] = e.Path
	}
	return files, nil
}

// head resolves branch to its commit.
func (u *upstream) head(ctx context.Context, repo, branch string) (string, error) {
	b, err := u.get(ctx, u.apiBase+"/repos/"+repo+"/commits/"+url.PathEscape(branch), "application/vnd.github.sha")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(b))
	if !commitHex.MatchString(sha) {
		return "", fmt.Errorf("%s %s: head %q is not a 40-hex commit", repo, branch, runesafe.SanitizeSingleLineBounded(sha, 80))
	}
	return sha, nil
}

func (u *upstream) cached(ctx context.Context, link, accept string) ([]byte, error) {
	if b, ok := u.cache[link]; ok {
		return b, nil
	}
	b, err := u.get(ctx, link, accept)
	if err != nil {
		return nil, err
	}
	u.cache[link] = b
	return b, nil
}

// get reads link, which is always the -raw or -api base joined with a
// manifest path and a commit or branch; the operator picks the bases.
func (u *upstream) get(ctx context.Context, link, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, http.NoBody) //nolint:gosec // G704: see get's doc
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.client.Do(req) //nolint:gosec // G704: see get's doc
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("fetch %s: %w", link, errNotFound)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("fetch %s: status %d", link, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", link, err)
	}
	if len(b) > maxFileBytes {
		return nil, fmt.Errorf("fetch %s: over %d bytes", link, maxFileBytes)
	}
	return b, nil
}

// csSymbol is one C# declaration's imported content.
type csSymbol struct {
	pattern     string
	options     []string
	replacement string
	values      []string
	members     []enumMember
}

type enumMember struct {
	name  string
	value int
}

// The manifest's symbol kinds.
const (
	kindRegex   = "regex"
	kindReplace = "replace"
	kindList    = "list"
	kindEnum    = "enum"
)

// declTypes are the C# declarations each manifest kind reads.
var declTypes = map[string][]string{
	kindRegex:   {"Regex"},
	kindReplace: {"RegexReplace"},
	kindList:    {"HashSet<string>", "Dictionary<string,"},
	kindEnum:    {"enum"},
}

// extractSymbol reads symbol's declaration from a C# file. A symbol absent,
// or declared with another type, is "renamed or removed".
func extractSymbol(src []byte, symbol, kind string) (csSymbol, error) {
	toks, err := lexCSharp(string(src))
	if err != nil {
		return csSymbol{}, err
	}
	at := findDecl(toks, symbol, declTypes[kind])
	if at < 0 {
		return csSymbol{}, errRenamed
	}
	r := &tokReader{toks: toks, pos: at}
	switch kind {
	case kindRegex, kindReplace:
		return r.regexDecl(kind == kindReplace)
	case kindList:
		return r.listDecl()
	default:
		return r.enumDecl()
	}
}

var errRenamed = errors.New("renamed or removed")

type tokKind uint8

const (
	tokIdent tokKind = iota
	tokString
	tokPunct
)

// csToken is one token; start and end are its byte offsets in the source.
type csToken struct {
	text       string
	kind       tokKind
	start, end int
}

// lexCSharp splits C# source into identifiers (dots and generic arguments
// joined, so HashSet<string> is one token), decoded strings and punctuation,
// dropping whitespace and comments.
func lexCSharp(src string) ([]csToken, error) {
	toks, _, err := lexSpans(src)
	return toks, err
}

// lexSpans is lexCSharp that also returns each comment's byte span.
func lexSpans(src string) (toks []csToken, comments [][2]int, err error) {
	for i := 0; i < len(src); {
		n, tok, err := lexOne(src[i:])
		if err != nil {
			return nil, nil, err
		}
		switch {
		case tok != nil:
			tok.start, tok.end = i, i+n
			toks = append(toks, *tok)
		case strings.HasPrefix(src[i:], "//") || strings.HasPrefix(src[i:], "/*"):
			comments = append(comments, [2]int{i, i + n})
		}
		i += n
	}
	return toks, comments, nil
}

// lexOne reads the token at the start of s, or skips one space, comment or
// character literal; it returns the bytes consumed.
func lexOne(s string) (int, *csToken, error) {
	switch {
	case isSpace(s[0]):
		return 1, nil, nil
	case strings.HasPrefix(s, "//"):
		if end := strings.IndexByte(s, '\n'); end >= 0 {
			return end, nil, nil
		}
		return len(s), nil, nil
	case strings.HasPrefix(s, "/*"):
		end := strings.Index(s[2:], "*/")
		if end < 0 {
			return 0, nil, errors.New("unterminated comment")
		}
		return end + 4, nil, nil
	case strings.HasPrefix(s, `"""`):
		return 0, nil, errors.New("raw string literals are not read")
	case strings.HasPrefix(s, `@"`):
		str, n, err := verbatimString(s)
		return n, &csToken{kind: tokString, text: str}, err
	case s[0] == '"':
		str, n, err := regularString(s)
		return n, &csToken{kind: tokString, text: str}, err
	case s[0] == '\'':
		off := 1
		if strings.HasPrefix(s[1:], `\`) {
			off = 3
		}
		end := strings.IndexByte(s[min(off, len(s)):], '\'')
		if end < 0 {
			return 0, nil, errors.New("unterminated character literal")
		}
		return off + end + 1, nil, nil
	}
	if n := identLen(s); n > 0 {
		return n, &csToken{kind: tokIdent, text: s[:n]}, nil
	}
	return 1, &csToken{kind: tokPunct, text: s[:1]}, nil
}

func identLen(s string) int {
	n := 0
	for n < len(s) {
		r, w := utf8.DecodeRuneInString(s[n:])
		if r != '_' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		n += w
	}
	if n > 0 && n < len(s) && s[n] == '<' {
		if end := strings.IndexByte(s[n:], '>'); end > 0 && !strings.ContainsAny(s[n:n+end], "();{}\"") {
			n += end + 1
		}
	}
	return n
}

// verbatimString decodes @"..." at the start of s, where "" is a quote.
func verbatimString(s string) (text string, n int, err error) {
	var b strings.Builder
	for i := 2; i < len(s); i++ {
		switch {
		case s[i] != '"':
			b.WriteByte(s[i])
		case i+1 < len(s) && s[i+1] == '"':
			b.WriteByte('"')
			i++
		default:
			return b.String(), i + 1, nil
		}
	}
	return "", 0, errors.New("unterminated verbatim string")
}

// regularString decodes "..." at the start of s.
func regularString(s string) (text string, n int, err error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '"':
			return b.String(), i + 1, nil
		case '\n':
			return "", 0, errors.New("newline in string")
		case '\\':
			n, err := csEscape(&b, s[i+1:])
			if err != nil {
				return "", 0, err
			}
			i += n
		default:
			b.WriteByte(s[i])
		}
	}
	return "", 0, errors.New("unterminated string")
}

func csEscape(b *strings.Builder, s string) (int, error) {
	if s == "" {
		return 0, errors.New("trailing escape")
	}
	simple := map[byte]byte{'\\': '\\', '"': '"', '\'': '\'', 'n': '\n', 't': '\t', 'r': '\r', '0': 0}
	if c, ok := simple[s[0]]; ok {
		b.WriteByte(c)
		return 1, nil
	}
	if s[0] == 'u' && len(s) >= 5 {
		v, err := strconv.ParseUint(s[1:5], 16, 16)
		if err != nil {
			return 0, err
		}
		b.WriteRune(rune(v))

		return 5, nil
	}
	return 0, fmt.Errorf(`escape \%c is not read`, s[0])
}

// findDecl returns the index after "<type> symbol =" (or "enum symbol"),
// or -1.
func findDecl(toks []csToken, symbol string, types []string) int {
	for i := 1; i < len(toks); i++ {
		if toks[i].kind != tokIdent || toks[i].text != symbol || toks[i-1].kind != tokIdent {
			continue
		}
		typ := toks[i-1].text
		for _, t := range types {
			if typ == t || strings.HasSuffix(t, ",") && strings.HasPrefix(typ, t) {
				return i + 1
			}
		}
	}
	return -1
}

type tokReader struct {
	toks []csToken
	pos  int
}

func (r *tokReader) next() (csToken, bool) {
	if r.pos >= len(r.toks) {
		return csToken{}, false
	}
	t := r.toks[r.pos]
	r.pos++
	return t, true
}

func (r *tokReader) expect(text string) error {
	t, ok := r.next()
	if !ok || t.text != text {
		return fmt.Errorf("expected %q, got %q", text, t.text)
	}
	return nil
}

// regexDecl reads "= new[ Regex](@"...", [replacement,] options)".
func (r *tokReader) regexDecl(replace bool) (csSymbol, error) {
	if err := r.expect("="); err != nil {
		return csSymbol{}, err
	}
	if t, _ := r.next(); t.text != "new" {
		return csSymbol{}, errors.New("not a constructor call")
	}
	t, _ := r.next()
	if t.kind == tokIdent {
		t, _ = r.next()
	}
	if t.text != "(" {
		return csSymbol{}, errors.New("not a constructor call")
	}
	var sym csSymbol
	pat, _ := r.next()
	if pat.kind != tokString {
		return csSymbol{}, errors.New("the pattern is not one string literal")
	}
	sym.pattern = pat.text
	if replace {
		if err := r.expect(","); err != nil {
			return csSymbol{}, err
		}
		rep, _ := r.next()
		switch {
		case rep.kind == tokString:
			sym.replacement = rep.text
		case rep.text != "string.Empty":
			return csSymbol{}, fmt.Errorf("replacement %q is not a literal", rep.text)
		}
	}
	opts, err := r.options()
	if err != nil {
		return csSymbol{}, err
	}
	sym.options = opts
	return sym, nil
}

func (r *tokReader) options() ([]string, error) {
	var opts []string
	for {
		t, ok := r.next()
		switch {
		case !ok:
			return nil, errors.New("unterminated constructor call")
		case t.text == ")":
			return opts, nil
		case t.text == "," || t.text == "|":
		case t.kind == tokIdent && strings.HasPrefix(t.text, "RegexOptions."):
			opts = append(opts, strings.TrimPrefix(t.text, "RegexOptions."))
		default:
			return nil, fmt.Errorf("argument %q is not read (a concatenated pattern?)", t.text)
		}
	}
}

// listDecl reads a HashSet's strings, or a Dictionary's string keys, from the
// collection initializer.
func (r *tokReader) listDecl() (csSymbol, error) {
	for {
		t, ok := r.next()
		if !ok || t.text == ";" {
			return csSymbol{}, errors.New("no collection initializer")
		}
		if t.text == "{" {
			break
		}
	}
	var sym csSymbol
	for depth := 1; depth > 0; {
		t, ok := r.next()
		switch {
		case !ok:
			return csSymbol{}, errors.New("unterminated initializer")
		case t.text == "{":
			depth++
			if r.pos < len(r.toks) && r.toks[r.pos].kind == tokString {
				sym.values = append(sym.values, r.toks[r.pos].text)
				r.pos++
			}
		case t.text == "}":
			depth--
		case t.kind == tokString && depth == 1:
			sym.values = append(sym.values, t.text)
		}
	}
	return sym, nil
}

// enumDecl reads members and their values, implicit ones counting on.
func (r *tokReader) enumDecl() (csSymbol, error) {
	if err := r.expect("{"); err != nil {
		return csSymbol{}, err
	}
	var sym csSymbol
	next := 0
	for {
		t, ok := r.next()
		switch {
		case !ok:
			return csSymbol{}, errors.New("unterminated enum")
		case t.text == "}":
			return sym, nil
		case t.text == ",":
			continue
		case t.kind != tokIdent:
			return csSymbol{}, fmt.Errorf("enum member %q is not read", t.text)
		}
		if r.pos < len(r.toks) && r.toks[r.pos].text == "=" {
			r.pos++
			v, _ := r.next()
			n, err := strconv.Atoi(v.text)
			if err != nil {
				return csSymbol{}, fmt.Errorf("enum value %q is not a number", v.text)
			}
			next = n
		}
		sym.members = append(sym.members, enumMember{name: t.text, value: next})
		next++
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
