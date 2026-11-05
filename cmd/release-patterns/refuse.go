package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/release/netregex"
)

// refusal is one reason generate writes nothing.
type refusal struct {
	id     string
	reason string
}

func (r refusal) String() string { return r.id + ": " + r.reason }

var commitHex = regexp.MustCompile(`^[0-9a-f]{40}$`)

func checkPin(name, value string) *refusal {
	if !commitHex.MatchString(value) {
		return &refusal{id: name, reason: fmt.Sprintf("pin %q is not a 40-hex commit", value)}
	}
	return nil
}

func patternSHA256(pattern string) string {
	sum := sha256.Sum256([]byte(pattern))
	return hex.EncodeToString(sum[:])
}

// checkOverride checks a declared override was written from this upstream
// text, so an upstream edit fails here instead of keeping a stale hand port.
func checkOverride(id, name, pattern string, overrides map[string]string) *refusal {
	have, ok := overrides[name]
	if !ok {
		return &refusal{id: id, reason: "no override " + name + " in " + overrideFile}
	}
	if sum := patternSHA256(pattern); have != sum {
		return &refusal{id: id, reason: fmt.Sprintf("the upstream pattern changed (sha256 %s): update override %s", sum, name)}
	}
	return nil
}

// unusedOverrides refuses an override no manifest entry names: its upstream
// text left the import, so the port is dead.
func unusedOverrides(overrides map[string]string, used map[string]bool) []refusal {
	var out []refusal
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		if !used[name] {
			out = append(out, refusal{id: name, reason: "serves no manifest entry: delete it from " + overrideFile})
		}
	}
	return out
}

// knownOptions maps the RegexOptions read to the executor's: Compiled
// changes no match, and the executor folds case invariantly, as
// CultureInvariant asks.
var knownOptions = map[string]func(*netregex.Options){
	"IgnoreCase":              func(o *netregex.Options) { o.IgnoreCase = true },
	"IgnorePatternWhitespace": func(o *netregex.Options) { o.IgnorePatternWhitespace = true },
	"Compiled":                func(*netregex.Options) {},
	"CultureInvariant":        func(*netregex.Options) {},
}

// checkOptions compares the upstream options with the manifest's, which name
// every option but Compiled; any other option (Multiline, Singleline,
// ExplicitCapture, RightToLeft, ECMAScript, NonBacktracking) is refused.
func checkOptions(id string, declared, upstream []string) (netregex.Options, *refusal) {
	var opts netregex.Options
	var got []string
	for _, o := range upstream {
		set, ok := knownOptions[o]
		if !ok {
			return netregex.Options{}, &refusal{id: id, reason: "RegexOptions." + o + " is not supported"}
		}
		set(&opts)
		if o != "Compiled" {
			got = append(got, o)
		}
	}
	slices.Sort(got)
	want := slices.Sorted(slices.Values(declared))
	if !slices.Equal(got, want) {
		return netregex.Options{}, &refusal{id: id, reason: fmt.Sprintf("options changed: upstream %v, manifest %v", got, want)}
	}
	return opts, nil
}

// checkGroups compares the pattern's named groups with the ones the manifest
// reads; a new source group needs a family decision before it is imported.
func checkGroups(id string, re *netregex.Regexp, declared []string) *refusal {
	upstream := slices.Sorted(slices.Values(re.SubexpNames()[1:]))
	want := slices.Sorted(slices.Values(declared))
	if !slices.Equal(upstream, want) {
		return &refusal{id: id, reason: fmt.Sprintf("named groups changed: upstream %v, manifest %v", upstream, want)}
	}
	return nil
}

// cfFile is a TRaSH custom format file, as far as the import reads it.
type cfFile struct {
	TrashID        string   `json:"trash_id"`
	Specifications []cfSpec `json:"specifications"`
}

type cfSpec struct {
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
	Fields         struct {
		Value json.RawMessage `json:"value"`
	} `json:"fields"`
	Negate   bool `json:"negate"`
	Required bool `json:"required"`
}

// fetchFormat reads one listed CF file and checks it is the one the manifest
// declares: the same trash_id and the same spec names.
func fetchFormat(ctx context.Context, up *upstream, repo, ref, id string, f *formatFile) (cfFile, *refusal) {
	b, err := up.file(ctx, repo, ref, f.Path)
	if errors.Is(err, errNotFound) {
		return cfFile{}, &refusal{id: id, reason: missingFormat(ctx, up, repo, ref, f)}
	}
	if err != nil {
		return cfFile{}, &refusal{id: id, reason: err.Error()}
	}
	var cf cfFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return cfFile{}, &refusal{id: id, reason: f.Path + ": " + err.Error()}
	}
	if cf.TrashID != f.TrashID {
		return cfFile{}, &refusal{id: id, reason: fmt.Sprintf("%s: trash_id %s, manifest %s: replaced upstream", f.Path, cf.TrashID, f.TrashID)}
	}
	names := make([]string, len(cf.Specifications))
	for i := range cf.Specifications {
		names[i] = cf.Specifications[i].Name
	}
	slices.Sort(names)
	if want := slices.Sorted(slices.Values(f.Specs)); !slices.Equal(names, want) {
		return cfFile{}, &refusal{id: id, reason: fmt.Sprintf("%s: specs %v, manifest %v: composition changed; review the CF and update the manifest", f.Path, names, want)}
	}
	return cf, nil
}

// missingFormat names a missing CF and, when the same trash_id sits at
// another path of its directory, where it moved.
func missingFormat(ctx context.Context, up *upstream, repo, ref string, f *formatFile) string {
	missing := f.Path + " missing"
	files, err := up.dir(ctx, repo, ref, path.Dir(f.Path))
	if err != nil {
		return missing + " (listing its directory: " + err.Error() + ")"
	}
	for _, p := range files {
		if !strings.HasSuffix(p, ".json") {
			continue
		}
		b, err := up.file(ctx, repo, ref, p)
		if err != nil {
			continue
		}
		var cf cfFile
		if json.Unmarshal(b, &cf) == nil && cf.TrashID == f.TrashID {
			return missing + ": moved to " + p
		}
	}
	return missing
}

// sourceValue reads a SourceSpecification's value, refusing one outside the
// arr's enum.
func sourceValue(id string, s *cfSpec, enum map[int]string) (string, *refusal) {
	var v int
	if err := json.Unmarshal(s.Fields.Value, &v); err != nil {
		return "", &refusal{id: id, reason: fmt.Sprintf("spec %s: value %s is not a number", s.Name, s.Fields.Value)}
	}
	member, ok := enum[v]
	if !ok {
		return "", &refusal{id: id, reason: fmt.Sprintf("spec %s: Source value %d is outside the arr's enum", s.Name, v)}
	}
	return member, nil
}

// checkMembers compares an enum's members with the manifest's mapping of
// each to subflux sources.
func checkMembers(id string, members []enumMember, declared map[string][]string) *refusal {
	got := make([]string, len(members))
	for i, m := range members {
		got[i] = m.name
	}
	slices.Sort(got)
	if want := slices.Sorted(maps.Keys(declared)); !slices.Equal(got, want) {
		return &refusal{id: id, reason: fmt.Sprintf("members changed: upstream %v, manifest %v: a new upstream source needs a family decision", got, want)}
	}
	return nil
}
