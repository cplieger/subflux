package language

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"strconv"

	"github.com/cplieger/langtag/v3"
)

// Policy is the parsed languages section: the targets each item wants and the
// close-varieties switch. It is immutable; the zero Policy has no targets,
// the switch off and a fixed digest.
type Policy struct{ p *policy }

type policy struct {
	defaults       []Target
	rules          []audioRule
	targets        []Target
	digest         [32]byte
	closeVarieties bool
}

// Target is one subtitle language a configuration asks for, with the variants
// wanted in it.
type Target struct {
	Tag      Tag
	Variants []Variant
}

// audioRule gives the targets of an item whose original or audio language is
// audio; an empty targets wants nothing.
type audioRule struct {
	audio   Tag
	targets []Target
}

// CloseVarieties reports the close-varieties switch: while on, a Substitute
// grade is automatic.
func (p Policy) CloseVarieties() bool { return p.p != nil && p.p.closeVarieties }

// TargetsFor answers the targets of an item: the closest-tier rule matching
// original, ties by config order, then each audio track the same way, else
// the default targets. A matched rule with no targets stops the chain, so the
// item wants nothing. The answer is the caller's to change.
func (p Policy) TargetsFor(original Tag, audio []Tag) []Target {
	if p.p == nil {
		return nil
	}
	for _, spoken := range append([]Tag{original}, audio...) {
		if r, ok := p.p.ruleFor(spoken); ok {
			return copyTargets(r.targets)
		}
	}
	return copyTargets(p.p.defaults)
}

// ruleFor is the rule whose audio language is closest to spoken within
// langtag's audio floor, TierOtherScript (cross-language tiers are written
// claims, not spoken ones); the first in config order wins a tie.
func (p *policy) ruleFor(spoken Tag) (audioRule, bool) {
	best, found := langtag.TierNone, false
	var match audioRule
	for _, r := range p.rules {
		if tier := langtag.Prefer(r.audio.t).Compare(spoken.t); tier <= langtag.TierOtherScript && tier < best {
			best, match, found = tier, r, true
		}
	}
	return match, found
}

// Targets is every configured target once, the defaults first then each
// rule's in order, a tag's variants the union of its listings in Variant
// order. The answer is the caller's to change.
func (p Policy) Targets() []Target {
	if p.p == nil {
		return nil
	}
	return copyTargets(p.p.targets)
}

func unionTargets(p *policy) []Target {
	var out []Target
	at := map[Tag]int{}
	add := func(t Target) {
		i, seen := at[t.Tag]
		if !seen {
			at[t.Tag] = len(out)
			out = append(out, Target{Tag: t.Tag})
			i = len(out) - 1
		}
		for _, v := range t.Variants {
			if !slices.Contains(out[i].Variants, v) {
				out[i].Variants = append(out[i].Variants, v)
			}
		}
	}
	for _, t := range p.defaults {
		add(t)
	}
	for _, r := range p.rules {
		for _, t := range r.targets {
			add(t)
		}
	}
	for i := range out {
		slices.Sort(out[i].Variants)
	}
	return out
}

func copyTargets(ts []Target) []Target {
	out := make([]Target, len(ts))
	for i, t := range ts {
		out[i] = Target{Tag: t.Tag, Variants: slices.Clone(t.Variants)}
	}
	return out
}

// Digest is a SHA-256 of the section as configured (the switch, every list,
// tag and variant, in order), so equal sections share it and any change moves
// it. It names the section only, never the grading code.
func (p Policy) Digest() [32]byte {
	if p.p == nil {
		return zeroDigest
	}
	return p.p.digest
}

var zeroDigest = digestOf(&policy{})

// digestFormat versions the encoding digestOf hashes; a change to the
// encoding bumps it, so no two encodings share a digest.
const digestFormat = "subflux languages digest v1"

func digestOf(p *policy) [32]byte {
	h := sha256.New()
	field := func(s string) {
		h.Write(binary.AppendUvarint(nil, uint64(len(s))))
		h.Write([]byte(s))
	}
	list := func(ts []Target) {
		field(strconv.Itoa(len(ts)))
		for _, t := range ts {
			field(t.Tag.String())
			field(strconv.Itoa(len(t.Variants)))
			for _, v := range t.Variants {
				field(strconv.Itoa(int(v)))
			}
		}
	}
	field(digestFormat)
	field(strconv.FormatBool(p.closeVarieties))
	list(p.defaults)
	field(strconv.Itoa(len(p.rules)))
	for _, r := range p.rules {
		field(r.audio.String())
		list(r.targets)
	}
	return [32]byte(h.Sum(nil))
}
