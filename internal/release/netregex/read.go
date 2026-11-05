package netregex

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// UnsupportedError names a .NET construct the executor does not run.
type UnsupportedError struct{ Construct string }

func (e *UnsupportedError) Error() string { return "unsupported construct " + e.Construct }

func unsupported(construct string) error { return &UnsupportedError{Construct: construct} }

// wordClass is .NET's \w as regexp2 defines it: letters, nonspacing marks,
// decimal digits, connector punctuation and the two zero-width joiners.
const wordClass = `\p{L}\p{Mn}\p{Nd}\p{Pc}\x{200C}\x{200D}`

// spaceClass is .NET's \s, char.IsWhiteSpace: Unicode's White_Space.
var spaceClass = func() string {
	var b strings.Builder
	for _, r := range unicode.White_Space.R16 {
		if r.Stride == 1 {
			fmt.Fprintf(&b, `\x{%X}-\x{%X}`, r.Lo, r.Hi)
			continue
		}
		for c := rune(r.Lo); c <= rune(r.Hi); c += rune(r.Stride) {
			fmt.Fprintf(&b, `\x{%X}`, c)
		}
	}
	return b.String()
}()

// reader turns .NET syntax into Go regexp/syntax text. Named groups become
// g<slot>_<n>, unnamed groups non-capturing, and each lookaround a
// placeholder capture p<k> whose body the compiler runs as predicate k.
type reader struct {
	c           *compiler
	src         string
	pos         int
	freeSpacing bool
	fold        bool // IgnoreCase in force here, which a lookaround body inherits
	inLook      bool // inside a lookaround, where a named group is refused
}

func (r *reader) read() (string, error) {
	text, err := r.alternation()
	if err != nil {
		return "", err
	}
	if r.pos < len(r.src) {
		return "", errors.New("unbalanced )")
	}
	return text, nil
}

func (r *reader) alternation() (string, error) {
	var b strings.Builder
	for r.pos < len(r.src) {
		c := r.src[r.pos]
		switch {
		case c == ')':
			return b.String(), nil
		case c == '|':
			r.pos++
			b.WriteByte('|')
		case r.freeSpacing && isSpace(c):
			r.pos++
		case r.freeSpacing && c == '#':
			for r.pos < len(r.src) && r.src[r.pos] != '\n' {
				r.pos++
			}
		default:
			text, err := r.quantified()
			if err != nil {
				return "", err
			}
			b.WriteString(text)
		}
	}
	return b.String(), nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// atom is one item's Go text; zero marks an assertion, which takes no
// quantifier.
type atom struct {
	text string
	zero bool
}

func (r *reader) quantified() (string, error) {
	a, err := r.atom()
	if err != nil || a.text == "" {
		return "", err
	}
	q := r.quantifier()
	if q != "" && a.zero {
		return "", unsupported("a quantified assertion")
	}
	return a.text + q, nil
}

func (r *reader) atom() (atom, error) {
	c := r.src[r.pos]
	switch c {
	case '(':
		return r.group()
	case '[':
		text, err := r.class()
		return atom{text: text}, err
	case '\\':
		return r.escape()
	case '^', '$':
		r.pos++
		return atom{text: string(c), zero: true}, nil
	case '*', '+', '?':
		return atom{}, errors.New("quantifier without an operand")
	case '{':
		r.pos++
		return atom{text: `\{`}, nil
	}
	_, w := utf8.DecodeRuneInString(r.src[r.pos:])
	text := r.src[r.pos : r.pos+w]
	r.pos += w
	return atom{text: text}, nil
}

var (
	namedGroup = regexp.MustCompile(`^\(\?(?:<([A-Za-z_][A-Za-z0-9_]*)>|'([A-Za-z_][A-Za-z0-9_]*)')`)
	flagGroup  = regexp.MustCompile(`^\(\?(i?)(?:-(i))?:`)
)

var lookarounds = []struct {
	open           string
	behind, negate bool
}{{"(?=", false, false}, {"(?!", false, true}, {"(?<=", true, false}, {"(?<!", true, true}}

func (r *reader) group() (atom, error) {
	rest := r.src[r.pos:]
	if m := namedGroup.FindStringSubmatch(rest); m != nil {
		if r.inLook {
			return atom{}, unsupported("a named group inside a lookaround")
		}
		r.pos += len(m[0])
		body, err := r.body()
		return atom{text: "(?P<" + r.c.capture(m[1]+m[2]) + ">" + body + ")"}, err
	}
	for _, la := range lookarounds {
		if strings.HasPrefix(rest, la.open) {
			r.pos += len(la.open)
			return r.lookaround(la.behind, la.negate)
		}
	}
	switch {
	case strings.HasPrefix(rest, "(?#"):
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return atom{}, errors.New("unterminated (?#")
		}
		r.pos += end + 1
		return atom{}, nil
	case strings.HasPrefix(rest, "(?:"):
		r.pos += 3
	case strings.HasPrefix(rest, "(?"):
		return r.flagGroup(rest)
	default:
		r.pos++
	}
	body, err := r.body()
	return atom{text: "(?:" + body + ")"}, err
}

// flagGroup reads (?i:...) and (?-i:...), the scoped IgnoreCase forms; any
// other inline option changes how the pattern parses or runs.
func (r *reader) flagGroup(rest string) (atom, error) {
	m := flagGroup.FindStringSubmatch(rest)
	if len(m) == 0 || m[1] == "" && m[2] == "" {
		return atom{}, unsupported(rest[:min(len(rest), 4)])
	}
	r.pos += len(m[0])
	outer := r.fold
	r.fold = m[1] != "" || outer && m[2] == ""
	defer func() { r.fold = outer }()
	body, err := r.body()
	return atom{text: m[0] + body + ")"}, err
}

func (r *reader) body() (string, error) {
	text, err := r.alternation()
	if err != nil {
		return "", err
	}
	if r.pos >= len(r.src) {
		return "", errors.New("missing )")
	}
	r.pos++
	return text, nil
}

func (r *reader) lookaround(behind, negate bool) (atom, error) {
	outer := r.inLook
	r.inLook = true
	text, err := r.body()
	r.inLook = outer
	if err != nil {
		return atom{}, err
	}
	if r.fold {
		text = "(?i:" + text + ")"
	}
	k := r.c.predicate(text, behind, negate)
	return atom{text: "(?P<p" + strconv.Itoa(k) + ">)", zero: true}, nil
}

func (r *reader) quantifier() string {
	if r.freeSpacing {
		for r.pos < len(r.src) && isSpace(r.src[r.pos]) {
			r.pos++
		}
	}
	if r.pos >= len(r.src) {
		return ""
	}
	var q string
	switch c := r.src[r.pos]; c {
	case '*', '+', '?':
		q = string(c)
		r.pos++
	case '{':
		m := counted.FindString(r.src[r.pos:])
		if m == "" {
			return ""
		}
		q = m
		r.pos += len(m)
	default:
		return ""
	}
	if r.pos < len(r.src) && r.src[r.pos] == '?' {
		q += "?"
		r.pos++
	}
	return q
}

var counted = regexp.MustCompile(`^\{\d+(?:,\d*)?\}`)

func (r *reader) escape() (atom, error) {
	if r.pos+1 >= len(r.src) {
		return atom{}, errors.New(`trailing \`)
	}
	c := r.src[r.pos+1]
	r.pos += 2
	switch c {
	case 'b', 'B', 'A', 'z':
		return atom{text: `\` + string(c), zero: true}, nil
	case 'Z':
		return atom{text: "$", zero: true}, nil
	case 'w':
		return atom{text: "[" + wordClass + "]"}, nil
	case 'W':
		return atom{text: "[^" + wordClass + "]"}, nil
	case 's':
		return atom{text: "[" + spaceClass + "]"}, nil
	case 'S':
		return atom{text: "[^" + spaceClass + "]"}, nil
	}
	text, err := r.escapeBody(c)
	return atom{text: text}, err
}

var plainEscapes = map[byte]string{
	'd': `\p{Nd}`, 'D': `\P{Nd}`, 't': `\t`, 'n': `\n`, 'r': `\r`, 'f': `\f`, 'v': `\v`, 'e': `\x{1B}`, 'a': `\x{07}`,
}

func (r *reader) escapeBody(c byte) (string, error) {
	if text, ok := plainEscapes[c]; ok {
		return text, nil
	}
	switch {
	case c == 'x':
		return r.hex(2)
	case c == 'u':
		return r.hex(4)
	case c == 'p' || c == 'P':
		return r.property(c)
	case c >= '0' && c <= '9' || c == 'k':
		return "", unsupported("a backreference")
	case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		return "", unsupported(`\` + string(c))
	case c >= utf8.RuneSelf:
		r.pos--
		ch, w := utf8.DecodeRuneInString(r.src[r.pos:])
		r.pos += w
		return regexp.QuoteMeta(string(ch)), nil
	}
	return `\` + string(c), nil
}

func (r *reader) property(c byte) (string, error) {
	end := strings.IndexByte(r.src[r.pos:], '}')
	if !strings.HasPrefix(r.src[r.pos:], "{") || end < 0 {
		return "", errors.New(`malformed \p`)
	}
	text := `\` + string(c) + r.src[r.pos:r.pos+end+1]
	r.pos += end + 1
	return text, nil
}

func (r *reader) hex(n int) (string, error) {
	if r.pos+n > len(r.src) {
		return "", errors.New("short hex escape")
	}
	digits := r.src[r.pos : r.pos+n]
	if strings.Trim(digits, "0123456789abcdefABCDEF") != "" {
		return "", errors.New("malformed hex escape")
	}
	r.pos += n
	return `\x{` + digits + `}`, nil
}

// class reads [...]. A '[' inside is escaped: Go reads [[:alpha:]] as a
// POSIX class where .NET reads the characters, and .NET's -[...]
// subtraction has no Go form.
func (r *reader) class() (string, error) {
	var b strings.Builder
	b.WriteByte('[')
	r.pos++
	if r.pos < len(r.src) && r.src[r.pos] == '^' {
		b.WriteByte('^')
		r.pos++
	}
	for first := true; r.pos < len(r.src); first = false {
		c := r.src[r.pos]
		switch {
		case c == ']' && !first:
			r.pos++
			b.WriteByte(']')
			return b.String(), nil
		case c == ']':
			b.WriteString(`\]`)
			r.pos++
		case c == '[':
			if strings.HasSuffix(b.String(), "-") && !strings.HasSuffix(b.String(), `\-`) {
				return "", unsupported("class subtraction")
			}
			b.WriteString(`\[`)
			r.pos++
		case c == '\\':
			text, err := r.classEscape()
			if err != nil {
				return "", err
			}
			b.WriteString(text)
		default:
			_, w := utf8.DecodeRuneInString(r.src[r.pos:])
			b.WriteString(r.src[r.pos : r.pos+w])
			r.pos += w
		}
	}
	return "", errors.New("unterminated [")
}

func (r *reader) classEscape() (string, error) {
	if r.pos+1 >= len(r.src) {
		return "", errors.New(`trailing \`)
	}
	c := r.src[r.pos+1]
	r.pos += 2
	switch c {
	case 'b':
		return `\x{08}`, nil
	case 'w':
		return wordClass, nil
	case 's':
		return spaceClass, nil
	case 'W', 'S':
		return "", unsupported(`\` + string(c) + " inside a class")
	}
	return r.escapeBody(c)
}
