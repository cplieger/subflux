package language

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

// section parses doc, a YAML document holding a languages key, and returns
// that key's value node; nil when doc has no languages key.
func section(t *testing.T, doc string) *yaml.Node {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if len(root.Content) == 0 {
		return nil
	}
	m := root.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == sectionKey {
			return m.Content[i+1]
		}
	}
	return nil
}

func mustPolicy(t *testing.T, doc string) Policy {
	t.Helper()
	p, errs := ParsePolicy(section(t, doc))
	if len(errs) > 0 {
		t.Fatalf("ParsePolicy: %v", errs)
	}
	return p
}

const validSection = `
languages:
  close_varieties: true
  default:
    - tag: pt-BR
      variants: [standard, forced]
    - tag: en
  rules:
    - audio: ja
      targets:
        - tag: en
          variants: [hi]
    - audio: es
      targets: []
`

func TestPreMarkedNodeParsesSame(t *testing.T) {
	loaded := mustPolicy(t, validSection)
	marked := section(t, validSection)
	retag(marked)
	premarked, errs := ParsePolicy(marked)
	if len(errs) > 0 {
		t.Fatalf("pre-marked: %v", errs)
	}
	quoted := mustPolicy(t, strings.Replace(validSection, "close_varieties: true", `close_varieties: "true"`, 1))
	nulls := section(t, validSection+"    - audio: fr\n      targets:\n        - tag: fr\n          variants:\n")
	retag(nulls)
	withNull, errs := ParsePolicy(nulls)
	if len(errs) > 0 {
		t.Fatalf("pre-marked null: %v", errs)
	}
	for name, p := range map[string]Policy{"pre-marked": premarked, "quoted true": quoted} {
		t.Run(name, func(t *testing.T) {
			if p.Digest() != loaded.Digest() || !p.CloseVarieties() {
				t.Errorf("digest %x, switch %v; want %x, true", p.Digest(), p.CloseVarieties(), loaded.Digest())
			}
		})
	}
	t.Run("pre-marked null reads as absent", func(t *testing.T) {
		if got := withNull.TargetsFor(mustParse(t, "fr"), nil); len(got) != 1 || !slices.Equal(got[0].Variants, []Variant{Standard}) {
			t.Errorf("TargetsFor(fr) = %v; want fr with the default variant", got)
		}
	})
}

// retag marks every scalar under n PlainTag, as a Document round trip does.
func retag(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode {
		n.Tag = formschema.PlainTag
	}
	for _, c := range n.Content {
		retag(c)
	}
}

func TestEveryScalarMarkedPlain(t *testing.T) {
	for name, doc := range map[string]string{
		"valid": validSection,
		"errors in every list": `
languages:
  close_varieties: maybe
  bogus: 1
  default:
    - tag: zz
      variants: [loud]
  rules:
    - audio: qq
      targets:
        - tag: pb
    - targets: {a: b}
`,
	} {
		t.Run(name, func(t *testing.T) {
			n := section(t, doc)
			_, errs := ParsePolicy(n)
			if name != "valid" && len(errs) == 0 {
				t.Fatal("no errors")
			}
			var unmarked []string
			walkScalars(n, func(s *yaml.Node) {
				if s.Tag != formschema.PlainTag {
					unmarked = append(unmarked, s.Value)
				}
			})
			if len(unmarked) > 0 {
				t.Errorf("scalars not marked %s: %q", formschema.PlainTag, unmarked)
			}
		})
	}
}

func walkScalars(n *yaml.Node, f func(*yaml.Node)) {
	if n.Kind == yaml.ScalarNode {
		f(n)
	}
	for _, c := range n.Content {
		walkScalars(c, f)
	}
}

func TestParsePolicyRows(t *testing.T) {
	const pre = "languages:\n  default:\n    - tag: en\n"
	target := func(tag string) string { return "languages:\n  default:\n    - tag: " + tag + "\n" }
	for _, r := range []struct{ name, doc, path, msg string }{
		{"absent section", "other: 1\n", "languages", "configure at least one subtitle target"},
		{"null section", "languages:\n", "languages", "configure at least one subtitle target"},
		{"not a mapping", "languages: [en]\n", "languages", "must be a mapping"},
		{"unknown key", pre + "  extra: 1\n", "languages", "unknown key extra"},
		{"no target anywhere", "languages:\n  default: []\n  rules:\n    - audio: ja\n      targets: []\n", "languages", "configure at least one subtitle target"},
		{"switch not a bool", pre + "  close_varieties: yes\n", "languages.close_varieties", "must be true or false"},
		{"switch a list", pre + "  close_varieties: [true]\n", "languages.close_varieties", "must be true or false"},
		{"default not a list", "languages:\n  default: en\n", "languages.default", "must be a list"},
		{"target not a mapping", "languages:\n  default: [en]\n", "languages.default[0]", "must be a mapping"},
		{"target unknown key", "languages:\n  default:\n    - tag: en\n      lang: en\n", "languages.default[0]", "unknown key lang"},
		{"too many targets", "languages:\n  default: [" + strings.Repeat("{tag: en},", 17) + "]\n", "languages.default", "at most 16 targets"},
		{"tag listed twice", "languages:\n  default:\n    - tag: en\n    - tag: en\n", "languages.default[1].tag", "en is listed twice"},
		{"tag absent", "languages:\n  default:\n    - variants: [hi]\n", "languages.default[0].tag", "tag is required"},
		{"tag empty", target(`""`), "languages.default[0].tag", "tag is required"},
		{"tag a list", target("[en]"), "languages.default[0].tag", "must be a single value"},
		{"bare zh", target("zh"), "languages.default[0].tag", "choose zh-Hans or zh-Hant"},
		{"chi folds to bare zh", target("chi"), "languages.default[0].tag", "choose zh-Hans or zh-Hant"},
		{"lat", target("lat"), "languages.default[0].tag", "use la"},
		{"spa-la", target("spa-la"), "languages.default[0].tag", "use es-419"},
		{"ku-IQ", target("ku-IQ"), "languages.default[0].tag", "use ku-Arab-IQ"},
		{"zh-TW", target("zh-TW"), "languages.default[0].tag", "use zh-Hant"},
		{"pb", target("pb"), "languages.default[0].tag", "use pt-BR"},
		{"zht", target("zht"), "languages.default[0].tag", "use zh-Hant"},
		{"cht", target("cht"), "languages.default[0].tag", "cht means Traditional Chinese in subtitle file names; choose zh-Hant"},
		{"spl", target("spl"), "languages.default[0].tag", "spl means Latin American Spanish in subtitle file names; choose es-419"},
		{"es-LA", target("es-LA"), "languages.default[0].tag", "es-LA means Latin American Spanish in subtitle file names; choose es-419"},
		{"not a tag", target("klingon"), "languages.default[0].tag", "not a language tag"},
		{"variants empty", "languages:\n  default:\n    - tag: en\n      variants: []\n", "languages.default[0].variants", "list at least one of standard, hi, forced"},
		{"variants not a list", "languages:\n  default:\n    - tag: en\n      variants: hi\n", "languages.default[0].variants", "must be a list"},
		{"unknown variant", "languages:\n  default:\n    - tag: en\n      variants: [sdh]\n", "languages.default[0].variants[0]", "unknown variant sdh"},
		{"variant listed twice", "languages:\n  default:\n    - tag: en\n      variants: [hi, hi]\n", "languages.default[0].variants[1]", "hi is listed twice"},
		{"rules not a list", pre + "  rules: {audio: ja}\n", "languages.rules", "must be a list"},
		{"too many rules", pre + "  rules: [" + rulesN(65) + "]\n", "languages.rules", "at most 64 rules"},
		{"rule unknown key", pre + "  rules:\n    - audio: ja\n      targets: []\n      when: always\n", "languages.rules[0]", "unknown key when"},
		{"targets absent", pre + "  rules:\n    - audio: ja\n", "languages.rules[0].targets", "targets is required"},
		{"audio absent", pre + "  rules:\n    - targets: []\n", "languages.rules[0].audio", "audio is required"},
		{"audio not canonical", pre + "  rules:\n    - audio: jpn\n      targets: []\n", "languages.rules[0].audio", "use ja"},
		{"audio not a tag", pre + "  rules:\n    - audio: qq\n      targets: []\n", "languages.rules[0].audio", "not a language tag"},
		{"audio twice", pre + "  rules:\n    - audio: ja\n      targets: []\n    - audio: ja\n      targets: []\n", "languages.rules[1].audio", "rule 0 already matches ja"},
		{"rule target error", pre + "  rules:\n    - audio: ja\n      targets:\n        - tag: zh\n", "languages.rules[0].targets[0].tag", "choose zh-Hans or zh-Hant"},
		{"33 distinct", "languages:\n  default: [" + targetsN(16, 0) + "]\n  rules:\n    - audio: ja\n      targets: [" + targetsN(16, 16) + "]\n    - audio: de\n      targets: [" + targetsN(1, 32) + "]\n", "languages", "at most 32 distinct subtitle languages"},
	} {
		t.Run(r.name, func(t *testing.T) {
			p, errs := ParsePolicy(section(t, r.doc))
			want := formschema.FieldError{Path: r.path, Message: r.msg}
			if !slices.Contains(errs, want) {
				t.Errorf("errors %v; want %+v", errs, want)
			}
			if p.Digest() != (Policy{}).Digest() || len(p.Targets()) != 0 {
				t.Errorf("a refused section answered a Policy with %d targets", len(p.Targets()))
			}
		})
	}
	t.Run("nil node", func(t *testing.T) {
		_, errs := ParsePolicy(nil)
		if want := []formschema.FieldError{{Path: "languages", Message: "configure at least one subtitle target"}}; !slices.Equal(errs, want) {
			t.Errorf("errors %v; want %v", errs, want)
		}
	})
	t.Run("every error at once", func(t *testing.T) {
		_, errs := ParsePolicy(section(t, "languages:\n  close_varieties: x\n  default:\n    - tag: zh\n    - tag: pb\n"))
		if len(errs) != 3 {
			t.Errorf("errors %v; want three", errs)
		}
	})
}

func TestParsePolicySectionChecksBesideFieldErrors(t *testing.T) {
	for _, r := range []struct {
		name, doc string
		want      []formschema.FieldError
	}{
		{"no target beside a bad switch", "languages:\n  close_varieties: maybe\n", []formschema.FieldError{
			{Path: "languages.close_varieties", Message: "must be true or false"},
			{Path: "languages", Message: "configure at least one subtitle target"},
		}},
		{"a duplicate after a row with bad variants", "languages:\n  default:\n    - tag: en\n      variants: [loud]\n    - tag: en\n", []formschema.FieldError{
			{Path: "languages.default[0].variants[0]", Message: "unknown variant loud"},
			{Path: "languages.default[1].tag", Message: "en is listed twice"},
		}},
		{"33 distinct beside bad variants", "languages:\n  default: [" + targetsN(16, 0) + "]\n  rules:\n    - audio: ja\n      targets: [" + targetsN(16, 16) + "]\n    - audio: de\n      targets:\n        - tag: " + targetTags[32] + "\n          variants: [loud]\n", []formschema.FieldError{
			{Path: "languages.rules[1].targets[0].variants[0]", Message: "unknown variant loud"},
			{Path: "languages", Message: "at most 32 distinct subtitle languages"},
		}},
		{"a refused target still counts as written", "languages:\n  default:\n    - tag: klingon\n", []formschema.FieldError{
			{Path: "languages.default[0].tag", Message: "not a language tag"},
		}},
		{"an over-long list still walks its rows", "languages:\n  default: [" + targetsN(16, 0) + ", {tag: " + targetTags[16] + ", variants: [loud]}]\n", []formschema.FieldError{
			{Path: "languages.default", Message: "at most 16 targets"},
			{Path: "languages.default[16].variants[0]", Message: "unknown variant loud"},
		}},
		{"an over-long rules list still walks its targets", "languages:\n  rules: [" + rulesUnique(65, "[{tag: fr}]") + "]\n", []formschema.FieldError{
			{Path: "languages.rules", Message: "at most 64 rules"},
		}},
	} {
		t.Run(r.name, func(t *testing.T) {
			if _, errs := ParsePolicy(section(t, r.doc)); !slices.Equal(errs, r.want) {
				t.Errorf("errors %v; want %v", errs, r.want)
			}
		})
	}
}

// The section checks read what the document holds: an unrelated field error,
// even one ending a list early, leaves their verdict unchanged.
func TestParsePolicySectionChecksIndependentOfOtherErrors(t *testing.T) {
	type verdict struct{ noTarget, tooManyTags bool }
	noTarget := formschema.FieldError{Path: "languages", Message: "configure at least one subtitle target"}
	tooMany := formschema.FieldError{Path: "languages", Message: "at most 32 distinct subtitle languages"}
	judge := func(t *testing.T, d sectionDoc) (verdict, int) {
		t.Helper()
		_, errs := ParsePolicy(section(t, d.text()))
		fieldErrs := 0
		for _, e := range errs {
			if e.Path != sectionKey {
				fieldErrs++
			}
		}
		return verdict{slices.Contains(errs, noTarget), slices.Contains(errs, tooMany)}, fieldErrs
	}
	ruleOver := func(from, n int) string { return "{audio: ja, targets: [" + targetsN(n, from) + "]}" }
	bases := []struct {
		name string
		doc  sectionDoc
		want verdict
	}{
		{"no target", sectionDoc{"true", nil, []string{"{audio: ja, targets: []}"}}, verdict{noTarget: true}},
		{"a default target", sectionDoc{"true", []string{"{tag: fr}"}, nil}, verdict{}},
		{"a rule target only", sectionDoc{"true", nil, []string{ruleOver(0, 1)}}, verdict{}},
		{"32 distinct", sectionDoc{"true", targetEntries(16, 0), []string{ruleOver(16, 16)}}, verdict{}},
		{"33 distinct", sectionDoc{"true", targetEntries(16, 0), []string{ruleOver(16, 16), "{audio: de, targets: [" + targetsN(1, 32) + "]}"}}, verdict{tooManyTags: true}},
	}
	perturbations := []struct {
		name         string
		apply        func(sectionDoc) sectionDoc
		needsDefault bool
	}{
		{"an invalid switch", func(d sectionDoc) sectionDoc { d.closeVarieties = "maybe"; return d }, false},
		{"a refused rule", func(d sectionDoc) sectionDoc {
			d.rules = padded(d.rules, "{audio: qq, targets: []}", len(d.rules)+1)
			return d
		}, false},
		{"an over-long rules list", func(d sectionDoc) sectionDoc {
			d.rules = padded(d.rules, "{audio: ja, targets: []}", maxRules+1)
			return d
		}, false},
		{"an over-long default list", func(d sectionDoc) sectionDoc {
			d.defaults = padded(d.defaults, d.defaults[0], maxListTargets+1)
			return d
		}, true},
	}
	for _, b := range bases {
		t.Run(b.name, func(t *testing.T) {
			if got, _ := judge(t, b.doc); got != b.want {
				t.Fatalf("base verdict %+v; want %+v", got, b.want)
			}
			for _, p := range perturbations {
				if p.needsDefault && len(b.doc.defaults) == 0 {
					continue
				}
				t.Run(p.name, func(t *testing.T) {
					got, fieldErrs := judge(t, p.apply(b.doc))
					if fieldErrs == 0 {
						t.Fatal("the perturbation added no field error")
					}
					if got != b.want {
						t.Errorf("verdict %+v; want %+v", got, b.want)
					}
				})
			}
		})
	}
}

// sectionDoc is a languages section built from flow-style list entries.
type sectionDoc struct {
	closeVarieties  string
	defaults, rules []string
}

func (d sectionDoc) text() string {
	return "languages:\n  close_varieties: " + d.closeVarieties + "\n  default: [" + strings.Join(d.defaults, ", ") +
		"]\n  rules: [" + strings.Join(d.rules, ", ") + "]\n"
}

// padded is a copy of list grown to n entries with copies of entry.
func padded(list []string, entry string, n int) []string {
	out := slices.Clone(list)
	for len(out) < n {
		out = append(out, entry)
	}
	return out
}

// targetTags are 33 distinct pickable tags.
var targetTags = strings.Fields("en de fr es it pt nl sv da nb nn fi pl cs sk hu ro bg el ru uk tr ar he fa hi bn ta te ja ko vi th")

func targetEntries(n, from int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = "{tag: " + targetTags[from+i] + "}"
	}
	return out
}

func targetsN(n, from int) string { return strings.Join(targetEntries(n, from), ", ") }

func rulesN(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "{audio: %s, targets: []},", targetTags[i%len(targetTags)])
	}
	return b.String()
}

func TestParsePolicyAccepts(t *testing.T) {
	p := mustPolicy(t, validSection)
	t.Run("default variant", func(t *testing.T) {
		got := p.Targets()
		if len(got) != 2 || got[1].Tag.String() != "en" || !slices.Equal(got[1].Variants, []Variant{Standard, HI}) {
			t.Errorf("Targets() = %v; want pt-BR then en with standard (default) and hi", got)
		}
	})
	t.Run("an empty rule targets list", func(t *testing.T) {
		if got := p.TargetsFor(mustParse(t, "es"), nil); len(got) != 0 {
			t.Errorf("TargetsFor(es) = %v; want nothing", got)
		}
	})
	t.Run("audio bare zh", func(t *testing.T) {
		q := mustPolicy(t, "languages:\n  rules:\n    - audio: zh\n      targets:\n        - tag: zh-Hant\n")
		if got := q.TargetsFor(mustParse(t, "cmn"), nil); len(got) != 1 || got[0].Tag.String() != "zh-Hant" {
			t.Errorf("TargetsFor(cmn) = %v; want zh-Hant", got)
		}
	})
	t.Run("32 distinct", func(t *testing.T) {
		mustPolicy(t, "languages:\n  default: ["+targetsN(16, 0)+"]\n  rules:\n    - audio: ja\n      targets: ["+targetsN(16, 16)+"]\n")
	})
	t.Run("64 rules", func(t *testing.T) {
		mustPolicy(t, "languages:\n  default: [{tag: en}]\n  rules: ["+rulesUnique(64, "[]")+"]\n")
	})
}

// rulesUnique are n rules, each over a distinct audio language and holding
// targets, a flow list.
func rulesUnique(n int, targets string) string {
	var b strings.Builder
	langs := primaryCandidates()
	for i := 0; n > 0; i++ {
		tag, ok := Parse(langs[i])
		if !ok || tag.String() != langs[i] || tag.t.MultiScript() {
			continue
		}
		fmt.Fprintf(&b, "{audio: %s, targets: %s},", langs[i], targets)
		n--
	}
	return b.String()
}

func TestCollidingTagsRefusedAsTargets(t *testing.T) {
	for _, tag := range collidingTags {
		t.Run(tag, func(t *testing.T) {
			_, errs := ParsePolicy(section(t, "languages:\n  default:\n    - tag: "+tag+"\n"))
			if len(errs) != 1 || !strings.Contains(errs[0].Message, "in subtitle file names; choose") {
				t.Errorf("errors %v; want the collision refusal", errs)
			}
		})
	}
}
