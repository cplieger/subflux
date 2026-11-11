package search

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/rank"
	"go.yaml.in/yaml/v4"
)

// Config is the search section. TargetMinFit keys are configured target
// tags; neither level is rank.Conflict.
type Config struct {
	// TargetMinFit replaces MinFit for its target.
	TargetMinFit map[language.Tag]rank.Fit
	// ScanDelay spaces each provider's Automatic calls.
	ScanDelay time.Duration
	// UpgradeWindowDays is how long after a delivery a better fit replaces
	// it; 0 turns fit upgrades off.
	UpgradeWindowDays int
	MinFit            rank.Fit
}

const (
	sectionKey       = "search"
	keyMinFit        = "min_fit"
	keyTargetMinFit  = "min_fit_by_target"
	keyUpgradeWindow = "upgrade_window_days"
	keyScanDelay     = "scan_delay"
)

// The section's bounds.
const (
	maxUpgradeWindowDays = 365
	minScanDelay         = 5 * time.Second
	maxScanDelay         = 10 * time.Minute
)

// fitChoices are the fits a minimum may name: every grade but Conflict,
// which no run ever takes.
var fitChoices = func() []string {
	var out []string
	for f := rank.Other; f <= rank.Exact; f++ {
		b, err := f.MarshalText()
		if err != nil {
			panic(err)
		}
		out = append(out, string(b))
	}
	return out
}()

// Schema is the section's flat fields; min_fit_by_target, a mapping from
// target tag to fit, is walked and marked plain by ParseConfig itself.
var Schema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: keyMinFit, Label: "Minimum fit", Kind: formschema.Enum, Enum: fitChoices, Default: "other",
		Help: "The least a subtitle must match the video's release to be downloaded automatically: other, same source, same release or exact.",
	},
	{
		Key: keyUpgradeWindow, Label: "Upgrade window (days)", Kind: formschema.Int, Min: 0, Max: maxUpgradeWindowDays, Default: "7",
		Help: "For how many days after a download a better fitting subtitle replaces it. 0 turns these upgrades off.",
	},
	{
		Key: keyScanDelay, Label: "Scan delay", Kind: formschema.DurationMs,
		Min: minScanDelay.Milliseconds(), Max: maxScanDelay.Milliseconds(), Default: "5s",
		Help: "The least time between two automatic searches on one provider.",
	},
}}

// ParseConfig reads the search section, n being its node or nil when
// absent, against the parsed languages policy p, reporting every refusal at
// once at its absolute path: Schema's, and in min_fit_by_target a key that
// is no configured target, a key set twice, or a value that is no fit or is
// conflict. Every scalar it reads is marked plain: the section holds no
// secret.
func ParseConfig(n *yaml.Node, p language.Policy) (Config, []formschema.FieldError) {
	flat, byTarget, errs := splitSection(n)
	vals, ferrs := Schema.Decode(flat)
	errs = append(errs, ferrs...)
	targets, terrs := parseTargetFits(byTarget, p)
	errs = append(errs, terrs...)
	if len(errs) > 0 {
		return Config{}, formschema.Prefix(sectionKey, errs)
	}
	var minFit rank.Fit
	if err := minFit.UnmarshalText([]byte(vals.String(keyMinFit))); err != nil {
		panic(err) // the Enum admits fit names only
	}
	return Config{
		MinFit:            minFit,
		TargetMinFit:      targets,
		UpgradeWindowDays: int(vals.Int(keyUpgradeWindow)),
		ScanDelay:         vals.Duration(keyScanDelay),
	}, nil
}

// splitSection parts n into a mapping of its flat keys, for Schema's
// Decode, and its min_fit_by_target value, nil when absent. A node that is
// no mapping goes to Decode whole, which refuses it.
func splitSection(n *yaml.Node) (flat, byTarget *yaml.Node, errs []formschema.FieldError) {
	if n == nil || formschema.IsNull(n) || n.Kind != yaml.MappingNode {
		return n, nil, nil
	}
	flat = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch {
		case k.Kind != yaml.ScalarNode || k.Value != keyTargetMinFit:
			flat.Content = append(flat.Content, k, v)
		case byTarget != nil:
			errs = append(errs, formschema.FieldError{Path: keyTargetMinFit, Message: "key is set twice"})
		default:
			byTarget = v
		}
	}
	return flat, byTarget, errs
}

// parseTargetFits reads min_fit_by_target, its keys by their text through
// language.Parse and its values by their text as fits.
func parseTargetFits(n *yaml.Node, p language.Policy) (map[language.Tag]rank.Fit, []formschema.FieldError) {
	if n == nil || formschema.IsNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, []formschema.FieldError{{Path: keyTargetMinFit, Message: "must be a mapping from target tag to fit"}}
	}
	targets := p.Targets()
	out := map[language.Tag]rank.Fit{}
	var errs []formschema.FieldError
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			errs = append(errs, formschema.FieldError{Path: keyTargetMinFit, Message: "keys must be language tags"})
			continue
		}
		path := keyTargetMinFit + "." + k.Value
		fit, msg := targetFit(k.Value, v, targets, out)
		formschema.MarkPlain(v)
		if msg != "" {
			errs = append(errs, formschema.FieldError{Path: path, Message: msg})
			continue
		}
		tag, _ := language.Parse(k.Value)
		out[tag] = fit
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// targetFit reads one min_fit_by_target pair; a non-empty message refuses it.
func targetFit(key string, v *yaml.Node, targets []language.Target, seen map[language.Tag]rank.Fit) (fit rank.Fit, msg string) {
	tag, ok := language.Parse(key)
	switch {
	case !ok:
		return 0, "is not a language tag"
	case !slices.ContainsFunc(targets, func(t language.Target) bool { return t.Tag == tag }):
		return 0, "is not a target the languages section configures"
	}
	if _, twice := seen[tag]; twice {
		return 0, "names a target set earlier"
	}
	if v.Kind != yaml.ScalarNode || fit.UnmarshalText([]byte(v.Value)) != nil || !validFit(fit) {
		return 0, "must be one of " + strings.Join(fitChoices, ", ")
	}
	return fit, ""
}

// errConfig is New's refusal of a Config no ParseConfig returns.
var errConfig = errors.New("search: config out of bounds")

// check repeats ParseConfig's refusals on a Config built some other way.
func (c *Config) check(p language.Policy) error {
	if !validFit(c.MinFit) {
		return fmt.Errorf("%w: min_fit %d", errConfig, c.MinFit)
	}
	targets := p.Targets()
	for tag, fit := range c.TargetMinFit {
		if !slices.ContainsFunc(targets, func(t language.Target) bool { return t.Tag == tag }) || !validFit(fit) {
			return fmt.Errorf("%w: min_fit_by_target %s %d", errConfig, tag, fit)
		}
	}
	if c.UpgradeWindowDays < 0 || c.UpgradeWindowDays > maxUpgradeWindowDays {
		return fmt.Errorf("%w: upgrade_window_days %d", errConfig, c.UpgradeWindowDays)
	}
	if c.ScanDelay < minScanDelay || c.ScanDelay > maxScanDelay {
		return fmt.Errorf("%w: scan_delay %s", errConfig, c.ScanDelay)
	}
	return nil
}

func validFit(f rank.Fit) bool { return f >= rank.Other && f <= rank.Exact }
