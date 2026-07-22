//go:build functional

package functional

import (
	"cmp"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file implements the typed JSON helpers the suite's assertions are
// built on. Each helper mirrors one exact jq program the retired bash suite
// ran (the program is named in the helper's doc comment) with jq-1.7
// semantics, byte-for-byte: values decode with json.Number so numbers
// render exactly as they appear on the wire, strings render bare under raw
// (-r) mode, and everything else renders as pretty 2-space JSON (compact
// for the activity row, like -c).
//
// Error model: the bash suite always invoked jq with `2>/dev/null` and
// captured stdout, so ANY runtime evaluation error (or undecodable/empty
// input) collapses to an empty string. Malformed constant paths are
// programmer bugs and panic instead (a deliberate tripwire; the bash
// equivalent would be a jq compile error yielding "").
//
// The full contract is pinned by TestJSONHelpersOracle against
// testdata/jq-oracle.json (offline, no server needed).

// --- Core: decode, path walk, iteration, length -----------------------------

// decodeBody decodes the first JSON value in body with UseNumber, exactly
// as jq consumed the response: blank input produces no output, an
// undecodable body is an error, and trailing content after the first value
// is ignored.
func decodeBody(body string) (any, bool) {
	if strings.TrimSpace(body) == "" {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// parsePath parses the tiny constant path syntax the suite uses --
// `.a.b[0].c` chains of field names and non-negative indexes, `.` for
// identity -- into walkPath keys. Paths are call-site literals; a
// malformed one is a programmer bug and panics.
func parsePath(path string) []any {
	if path == "" || path[0] != '.' {
		panic("functional suite bug: bad JSON path " + strconv.Quote(path))
	}
	var keys []any
	i := 1
	for i < len(path) {
		switch path[i] {
		case '.':
			i++ // segment separator: ".a.b", ".[0].id"
		case '[':
			j := strings.IndexByte(path[i:], ']')
			if j < 0 {
				panic("functional suite bug: unterminated index in " + strconv.Quote(path))
			}
			n, err := strconv.Atoi(path[i+1 : i+j])
			if err != nil || n < 0 {
				panic("functional suite bug: bad index in " + strconv.Quote(path))
			}
			keys = append(keys, n)
			i += j + 1
		default:
			j := i
			for j < len(path) && path[j] != '.' && path[j] != '[' {
				j++
			}
			keys = append(keys, path[i:j])
			i = j
		}
	}
	return keys
}

// walkPath applies field/index keys with jq semantics: null propagates
// (`.missing.deeper` stays null), a missing object field yields null, an
// out-of-range index yields null, and indexing a wrong-typed value is an
// error (ok=false).
func walkPath(v any, keys []any) (any, bool) {
	for _, k := range keys {
		switch key := k.(type) {
		case string:
			switch t := v.(type) {
			case nil:
				// stays null
			case map[string]any:
				v = t[key]
			default:
				return nil, false
			}
		case int:
			switch t := v.(type) {
			case nil:
				// stays null
			case []any:
				if key >= len(t) {
					v = nil
				} else {
					v = t[key]
				}
			default:
				return nil, false
			}
		}
	}
	return v, true
}

// elemField is `.name` on one iterated element: null yields null, an
// object yields the (possibly absent -> null) field, anything else is an
// error.
func elemField(el any, name string) (any, bool) {
	return walkPath(el, []any{name})
}

// iterateValues is `.[]`: array elements in order, object values in sorted
// key order, error for anything else (including null).
func iterateValues(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case map[string]any:
		outs := make([]any, 0, len(t))
		for _, k := range jqSortedKeys(t) {
			outs = append(outs, t[k])
		}
		return outs, true
	default:
		return nil, false
	}
}

// decodeList is the shared decode+iterate front half of every `[.[] | ...]`
// helper.
func decodeList(body string) ([]any, bool) {
	v, ok := decodeBody(body)
	if !ok {
		return nil, false
	}
	return iterateValues(v)
}

// lengthOf is jq's `length`: null is 0, strings count runes, arrays and
// objects count elements, a number is its absolute value kept literal
// (sign stripped, no reformatting -- matches jq's output byte-for-byte),
// booleans have no length (error).
func lengthOf(v any) (json.Number, bool) {
	switch t := v.(type) {
	case nil:
		return json.Number("0"), true
	case string:
		return json.Number(strconv.Itoa(utf8.RuneCountInString(t))), true
	case []any:
		return json.Number(strconv.Itoa(len(t))), true
	case map[string]any:
		return json.Number(strconv.Itoa(len(t))), true
	case json.Number:
		return json.Number(strings.TrimPrefix(string(t), "-")), true
	default:
		return "", false
	}
}

// --- Path-shaped helpers -----------------------------------------------------

// lookup is the shared front half of every path helper: decode, walk,
// collapse errors to ok=false.
func lookup(body, path string) (any, bool) {
	v, ok := decodeBody(body)
	if !ok {
		return nil, false
	}
	return walkPath(v, parsePath(path))
}

// fieldRaw mirrors `jq -r '<path>'`: strings bare, other values pretty
// JSON, "" on error.
func fieldRaw(body, path string) string {
	v, ok := lookup(body, path)
	if !ok {
		return ""
	}
	return jqRender(v, true, false)
}

// fieldJSON mirrors flagless `jq '<path>'`: every value as pretty JSON.
func fieldJSON(body, path string) string {
	v, ok := lookup(body, path)
	if !ok {
		return ""
	}
	return jqRender(v, false, false)
}

// fieldAlt is `<path> // <alt>` with alt pre-rendered: a truthy value
// renders normally, null/false yield alt, errors yield "".
func fieldAlt(body, path string, raw bool, alt string) string {
	v, ok := lookup(body, path)
	if !ok {
		return ""
	}
	if jqTruthy(v) {
		return jqRender(v, raw, false)
	}
	return alt
}

// fieldRawOrEmpty mirrors `jq -r '<path> // empty'`. Note it swallows a
// literal false, exactly like jq's `//`.
func fieldRawOrEmpty(body, path string) string { return fieldAlt(body, path, true, "") }

// fieldJSONOrEmpty mirrors `jq '<path> // empty'`.
func fieldJSONOrEmpty(body, path string) string { return fieldAlt(body, path, false, "") }

// fieldRawOrFalse mirrors `jq -r '<path> // false'`.
func fieldRawOrFalse(body, path string) string { return fieldAlt(body, path, true, "false") }

// fieldJSONOrFalse mirrors `jq '<path> // false'`.
func fieldJSONOrFalse(body, path string) string { return fieldAlt(body, path, false, "false") }

// fieldJSONOrZero mirrors `jq '<path> // 0'`.
func fieldJSONOrZero(body, path string) string { return fieldAlt(body, path, false, "0") }

// fieldNotNull mirrors `jq '<path> != null'`: "true"/"false", "" on error.
func fieldNotNull(body, path string) string {
	v, ok := lookup(body, path)
	if !ok {
		return ""
	}
	return jqRender(v != nil, false, false)
}

// lengthAt mirrors `jq '<path> | length'` (and bare `length` via path ".").
func lengthAt(body, path string) string {
	v, ok := lookup(body, path)
	if !ok {
		return ""
	}
	n, ok := lengthOf(v)
	if !ok {
		return ""
	}
	return jqRender(n, false, false)
}

// resultsLen mirrors `jq '.results | length'` (the search-result count).
func resultsLen(body string) string { return lengthAt(body, ".results") }

// --- Iteration-shaped helpers ------------------------------------------------

// countFieldEq mirrors `jq '[.[] | select(.<field>=="<want>")] | length'`:
// count elements whose field equals the string. The collect-then-count
// shape means an uniterable element anywhere errors the whole program.
func countFieldEq(body, field, want string) string {
	items, ok := decodeList(body)
	if !ok {
		return ""
	}
	n := 0
	for _, el := range items {
		fv, ok := elemField(el, field)
		if !ok {
			return ""
		}
		if s, isStr := fv.(string); isStr && s == want {
			n++
		}
	}
	return strconv.Itoa(n)
}

// schemaProviderCount mirrors
// `jq '[.[] | select(.key=="providers") | .providers[] | select(.name=="<name>")] | length'`
// over the settings schema: count providers with the given name inside
// key=="providers" sections.
func schemaProviderCount(body, name string) string {
	sections, ok := decodeList(body)
	if !ok {
		return ""
	}
	n := 0
	for _, sec := range sections {
		kv, ok := elemField(sec, "key")
		if !ok {
			return ""
		}
		if s, isStr := kv.(string); !isStr || s != "providers" {
			continue
		}
		pv, ok := elemField(sec, "providers")
		if !ok {
			return ""
		}
		provs, ok := iterateValues(pv)
		if !ok {
			return ""
		}
		for _, p := range provs {
			nv, ok := elemField(p, "name")
			if !ok {
				return ""
			}
			if s, isStr := nv.(string); isStr && s == name {
				n++
			}
		}
	}
	return strconv.Itoa(n)
}

// seriesPick mirrors
// `jq '[.[] | select(.episodes > 0 and .episodes <= 10)] | .[0].id // empty'`:
// the id of the first series with 1-10 episodes. The comparisons use jq's
// total order over ALL values (null < false < true < numbers < strings <
// arrays < objects), so an entry with a missing episodes field compares as
// null and is filtered out rather than erroring. The bracket collect means
// every element is visited even after a match; an error anywhere yields "".
func seriesPick(body string) string {
	items, ok := decodeList(body)
	if !ok {
		return ""
	}
	var first any
	found := false
	for _, el := range items {
		ev, ok := elemField(el, "episodes")
		if !ok {
			return ""
		}
		if !found && jqOrder(ev, json.Number("0")) > 0 && jqOrder(ev, json.Number("10")) <= 0 {
			first, found = el, true
		}
	}
	if !found {
		return "" // .[0] of no matches -> null -> .id -> null -> empty
	}
	idv, _ := elemField(first, "id") // matched elements are objects
	if !jqTruthy(idv) {
		return ""
	}
	return jqRender(idv, false, false)
}

// activityRow mirrors `jq -c --arg id <id> '[.[] | select(.id == $id)] | .[0] // empty'`:
// the first activity entry whose id equals the --arg string, rendered
// compact ("" when absent). --arg values are string-typed, so a numeric
// wire id never matches.
func activityRow(body, id string) string {
	items, ok := decodeList(body)
	if !ok {
		return ""
	}
	var first any
	found := false
	for _, el := range items {
		fv, ok := elemField(el, "id")
		if !ok {
			return ""
		}
		if s, isStr := fv.(string); isStr && s == id && !found {
			first, found = el, true
		}
	}
	if !found {
		return ""
	}
	return jqRender(first, false, true)
}

// runningLen mirrors
// `jq -r --arg id <id> '[.[] | select(.id == $id and (.done | not))] | length'`:
// count live (not-done) entries with the given id. `and` short-circuits:
// .done is only read on id matches, and a missing .done counts as live.
func runningLen(body, id string) string {
	items, ok := decodeList(body)
	if !ok {
		return ""
	}
	n := 0
	for _, el := range items {
		fv, ok := elemField(el, "id")
		if !ok {
			return ""
		}
		s, isStr := fv.(string)
		if !isStr || s != id {
			continue
		}
		dv, _ := elemField(el, "done") // el is an object: cannot error
		if !jqTruthy(dv) {
			n++
		}
	}
	return strconv.Itoa(n)
}

// providerBreakdown mirrors the one aggregation pipeline the suite logs
// (real_providers):
//
//	jq -r '[.results[].provider] | group_by(.) | map({(.[0]): length}) | add // {}'
//
// Count occurrences per provider string and render one pretty object with
// sorted keys (the same order group_by's sort produces). Empty results
// render "{}"; a missing/uniterable results array, a non-object result, or
// a non-string provider all error to "".
func providerBreakdown(body string) string {
	v, ok := decodeBody(body)
	if !ok {
		return ""
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	results, ok := obj["results"].([]any)
	if !ok {
		return ""
	}
	counts := map[string]any{}
	for _, item := range results {
		entry, ok := item.(map[string]any)
		if !ok {
			return ""
		}
		provider, ok := entry["provider"].(string)
		if !ok {
			return ""
		}
		n, _ := counts[provider].(json.Number)
		cur, _ := n.Int64()
		counts[provider] = json.Number(strconv.FormatInt(cur+1, 10))
	}
	return jqRender(counts, true, false)
}

// --- Kept primitives (verbatim from the retired jq-subset interpreter) -------

// jqRender matches jq's output formatting: -r prints strings bare; every
// other value prints as JSON, pretty-printed with a 2-space indent (what
// flagless jq does — the real_providers per-provider breakdown relies on
// it) unless compact (-c). HTML escaping is off: jq never escapes <, >, &.
func jqRender(v any, raw, compact bool) string {
	if s, ok := v.(string); ok && raw {
		return s
	}
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if !compact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return ""
	}
	// Encoder appends a trailing newline jq's value rendering doesn't have.
	return strings.TrimSuffix(sb.String(), "\n")
}

// jqTruthy implements jq truthiness: only null and false are falsy.
func jqTruthy(v any) bool {
	if v == nil {
		return false
	}
	b, ok := v.(bool)
	return !ok || b
}

// jqTypeRank orders value kinds per jq's total order:
// null < false < true < numbers < strings < arrays < objects.
func jqTypeRank(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 2
		}
		return 1
	case json.Number:
		return 3
	case string:
		return 4
	case []any:
		return 5
	case map[string]any:
		return 6
	default:
		return 7
	}
}

// jqOrder compares two values under jq's total order, returning a
// negative/zero/positive result like cmp.Compare. Ordering comparisons
// never error in jq; seriesPick relies on that (a missing field compares
// as null and is filtered out, it does not error the whole pipeline).
func jqOrder(l, r any) int {
	if lr, rr := jqTypeRank(l), jqTypeRank(r); lr != rr {
		return cmp.Compare(lr, rr)
	}
	switch lv := l.(type) {
	case json.Number:
		lf, _ := lv.Float64()
		rf, _ := r.(json.Number).Float64()
		return cmp.Compare(lf, rf)
	case string:
		return strings.Compare(lv, r.(string))
	case []any:
		rv := r.([]any)
		for i := 0; i < len(lv) && i < len(rv); i++ {
			if c := jqOrder(lv[i], rv[i]); c != 0 {
				return c
			}
		}
		return cmp.Compare(len(lv), len(rv))
	case map[string]any:
		// jq compares objects by their sorted key sets first, then by
		// values key-by-key.
		rv := r.(map[string]any)
		lk, rk := jqSortedKeys(lv), jqSortedKeys(rv)
		for i := 0; i < len(lk) && i < len(rk); i++ {
			if c := strings.Compare(lk[i], rk[i]); c != 0 {
				return c
			}
		}
		if c := cmp.Compare(len(lk), len(rk)); c != 0 {
			return c
		}
		for _, k := range lk {
			if c := jqOrder(lv[k], rv[k]); c != 0 {
				return c
			}
		}
		return 0
	default:
		// null vs null, or equal bools (false/true already split by rank).
		return 0
	}
}

func jqSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
