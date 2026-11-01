package language

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

// The section's limits bound the planner's loops, far above any real
// configuration.
const (
	maxListTargets  = 16
	maxRules        = 64
	maxDistinctTags = 32
)

const (
	sectionKey     = "languages"
	msgNoTarget    = "configure at least one subtitle target"
	msgRequired    = "is required"
	msgMapping     = "must be a mapping"
	msgList        = "must be a list"
	msgSingleValue = "must be a single value"
)

var variantKeys = map[string]Variant{"standard": Standard, "hi": HI, "forced": Forced}

// ParsePolicy reads the languages section, n being its value node (nil when
// the section is absent), and returns every field error at once, each path
// absolute from the config root (languages.rules[1].targets[0].tag). On any
// error the Policy is the zero Policy. Every scalar is read by its text
// whatever its YAML tag, and afterwards, valid or not, every scalar under n
// is marked formschema.PlainTag: the section holds no secret.
func ParsePolicy(n *yaml.Node) (Policy, []formschema.FieldError) {
	p, errs := walkPolicy(n)
	formschema.MarkPlain(n)
	if len(errs) > 0 {
		return Policy{}, errs
	}
	p.targets = unionTargets(p)
	p.digest = digestOf(p)
	return Policy{p}, nil
}

// policyWalk's distinct holds every target tag that parses and written
// whether any target list holds an element, whether or not the row enters the
// Policy. No recorded error ends the walk: each step still returns what it
// read, or checkTargetCount's verdict would depend on how far the walk got.
type policyWalk struct {
	distinct map[Tag]bool
	errs     []formschema.FieldError
	written  bool
}

func (w *policyWalk) fail(path, msg string) {
	w.errs = append(w.errs, formschema.FieldError{Path: path, Message: msg})
}

func walkPolicy(n *yaml.Node) (*policy, []formschema.FieldError) {
	w := policyWalk{distinct: map[Tag]bool{}}
	if n == nil || formschema.IsNull(n) {
		w.fail(sectionKey, msgNoTarget)
		return nil, w.errs
	}
	fields, ok := w.mapping(n, sectionKey, "close_varieties", "default", "rules")
	if !ok {
		return nil, w.errs
	}
	p := &policy{}
	if v := fields["close_varieties"]; v != nil {
		p.closeVarieties = w.boolean(v, sectionKey+".close_varieties")
	}
	if v := fields["default"]; v != nil {
		p.defaults = w.targetList(v, sectionKey+".default")
	}
	if v := fields["rules"]; v != nil {
		p.rules = w.rules(v, sectionKey+".rules")
	}
	w.checkTargetCount()
	return p, w.errs
}

// mapping indexes n's keys among allowed, refusing a non-mapping, an unknown
// or repeated key and a key that is no plain scalar; a null value is absent.
func (w *policyWalk) mapping(n *yaml.Node, path string, allowed ...string) (map[string]*yaml.Node, bool) {
	if n.Kind != yaml.MappingNode {
		w.fail(path, msgMapping)
		return nil, false
	}
	fields := map[string]*yaml.Node{}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch {
		case k.Kind != yaml.ScalarNode:
			w.fail(path, "keys must be plain names")
		case !slices.Contains(allowed, k.Value):
			w.fail(path, "unknown key "+k.Value)
		case seen[k.Value]:
			w.fail(path+"."+k.Value, "key is set twice")
		default:
			seen[k.Value] = true
			if !formschema.IsNull(v) {
				fields[k.Value] = v
			}
		}
	}
	return fields, true
}

func (w *policyWalk) boolean(n *yaml.Node, path string) bool {
	if n.Kind == yaml.ScalarNode {
		if b, ok := formschema.ParseBool(n.Value); ok {
			return b
		}
	}
	w.fail(path, "must be true or false")
	return false
}

// sequence returns n's elements, false for a non-list. A list over a positive
// limit is refused and its elements are still returned.
func (w *policyWalk) sequence(n *yaml.Node, path string, limit int, overLimit string) ([]*yaml.Node, bool) {
	if n.Kind != yaml.SequenceNode {
		w.fail(path, msgList)
		return nil, false
	}
	if limit > 0 && len(n.Content) > limit {
		w.fail(path, overLimit)
	}
	return n.Content, true
}

func (w *policyWalk) targetList(n *yaml.Node, path string) []Target {
	if n.Kind == yaml.SequenceNode && len(n.Content) > 0 {
		w.written = true
	}
	elems, ok := w.sequence(n, path, maxListTargets, fmt.Sprintf("at most %d targets", maxListTargets))
	if !ok {
		return nil
	}
	targets := make([]Target, 0, len(elems))
	listed := map[Tag]bool{}
	for i, e := range elems {
		p := path + "[" + strconv.Itoa(i) + "]"
		fields, ok := w.mapping(e, p, "tag", "variants")
		if !ok {
			continue
		}
		tag, tagOK := w.targetTag(fields["tag"], p+".tag")
		variants, variantsOK := w.variants(fields["variants"], p+".variants")
		if !tagOK {
			continue
		}
		if listed[tag] {
			w.fail(p+".tag", tag.String()+" is listed twice")
			continue
		}
		listed[tag] = true
		w.distinct[tag] = true
		if variantsOK {
			targets = append(targets, Target{Tag: tag, Variants: variants})
		}
	}
	return targets
}

// targetTag reads a target's tag, refusing every spelling that is not one
// target's one canonical text.
func (w *policyWalk) targetTag(n *yaml.Node, path string) (Tag, bool) {
	text, ok := w.scalar(n, path)
	if !ok {
		return Tag{}, false
	}
	if msg := targetTagProblem(text); msg != "" {
		w.fail(path, msg)
		return Tag{}, false
	}
	tag, _ := Parse(text)
	return tag, true
}

// targetTagProblem is the message refusing text as a target tag, "" when it
// is one: bare zh claims no script; a tag whose file name reads back as
// another language (cht) is refused, so every target round-trips through its
// file name; and only when Parse refuses text is the read exception table
// consulted for the spelling to use.
func targetTagProblem(text string) string {
	tag, ok := Parse(text)
	switch {
	case !ok:
		if e, isException := readExceptions[asciiLower(text)]; isException {
			return "use " + e.tag.String()
		}
		return "not a language tag"
	case tag == (Tag{}):
		return "tag " + msgRequired
	case tag.String() == chineseLanguage:
		return "choose zh-Hans or zh-Hant"
	}
	if e, collides := readExceptions[asciiLower(tag.String())]; collides {
		if text != tag.String() {
			return "use " + e.tag.String()
		}
		return text + " means " + e.meaning + " in subtitle file names; choose " + e.tag.String()
	}
	if text != tag.String() {
		return "use " + tag.String()
	}
	return ""
}

func (w *policyWalk) variants(n *yaml.Node, path string) ([]Variant, bool) {
	if n == nil {
		return []Variant{Standard}, true
	}
	elems, ok := w.sequence(n, path, 0, "")
	if !ok {
		return nil, false
	}
	if len(elems) == 0 {
		w.fail(path, "list at least one of standard, hi, forced")
		return nil, false
	}
	out := make([]Variant, 0, len(elems))
	valid := true
	for i, e := range elems {
		p := path + "[" + strconv.Itoa(i) + "]"
		text, ok := w.scalar(e, p)
		v, known := variantKeys[text]
		switch {
		case !ok:
			valid = false
		case !known:
			w.fail(p, "unknown variant "+text)
			valid = false
		case slices.Contains(out, v):
			w.fail(p, text+" is listed twice")
			valid = false
		default:
			out = append(out, v)
		}
	}
	return out, valid
}

func (w *policyWalk) rules(n *yaml.Node, path string) []audioRule {
	elems, ok := w.sequence(n, path, maxRules, fmt.Sprintf("at most %d rules", maxRules))
	if !ok {
		return nil
	}
	rules := make([]audioRule, 0, len(elems))
	first := map[Tag]int{}
	for i, e := range elems {
		p := path + "[" + strconv.Itoa(i) + "]"
		fields, ok := w.mapping(e, p, "audio", "targets")
		if !ok {
			continue
		}
		audio, audioOK := w.audioTag(fields["audio"], p+".audio")
		if j, dup := first[audio]; audioOK && dup {
			w.fail(p+".audio", fmt.Sprintf("rule %d already matches %s", j, audio))
			audioOK = false
		}
		if audioOK {
			first[audio] = i
		}
		v := fields["targets"]
		if v == nil {
			w.fail(p+".targets", "targets "+msgRequired)
			continue
		}
		before := len(w.errs)
		targets := w.targetList(v, p+".targets")
		if audioOK && len(w.errs) == before {
			rules = append(rules, audioRule{audio: audio, targets: targets})
		}
	}
	return rules
}

// audioTag reads a rule's spoken language. Bare zh is allowed: it names
// spoken Mandarin.
func (w *policyWalk) audioTag(n *yaml.Node, path string) (Tag, bool) {
	text, ok := w.scalar(n, path)
	if !ok {
		return Tag{}, false
	}
	tag, parsed := Parse(text)
	switch {
	case !parsed:
		w.fail(path, "not a language tag")
	case tag == (Tag{}):
		w.fail(path, "audio "+msgRequired)
	case tag.String() != text:
		w.fail(path, "use "+tag.String())
	default:
		return tag, true
	}
	return Tag{}, false
}

// scalar is n's text; an absent key reads as "" for the caller's own
// required-field check, and a collection is refused.
func (w *policyWalk) scalar(n *yaml.Node, path string) (string, bool) {
	switch {
	case n == nil:
		return "", true
	case n.Kind != yaml.ScalarNode:
		w.fail(path, msgSingleValue)
		return "", false
	}
	return n.Value, true
}

func (w *policyWalk) checkTargetCount() {
	switch {
	case !w.written:
		w.fail(sectionKey, msgNoTarget)
	case len(w.distinct) > maxDistinctTags:
		w.fail(sectionKey, fmt.Sprintf("at most %d distinct subtitle languages", maxDistinctTags))
	}
}
