package timing_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// code is the element type of timing.Codes.
type code = struct {
	Err  error
	Code string
}

// failureArm is one way an exported call fails.
type failureArm struct {
	name string
	call func() error
}

func failureArms(t testing.TB) []failureArm {
	t.Helper()
	var arms []failureArm
	for _, tc := range invalidTimings {
		arms = append(arms, failureArm{"Validate " + tc.name, func() error { return timing.Validate(tc.t) }})
	}
	for _, tc := range undecodable {
		arms = append(arms, failureArm{"DecodeTiming " + tc.name, func() error {
			_, err := timing.DecodeTiming(tc.b)
			return err
		}})
	}
	for _, tc := range refusedDocs(t) {
		arms = append(arms, failureArm{"ParseCues " + tc.name, func() error {
			_, err := timing.ParseCues(tc.b)
			return err
		}})
	}
	for _, tc := range undecodableCues(t) {
		arms = append(arms, failureArm{"DecodeCues " + tc.name, func() error {
			_, err := timing.DecodeCues(tc.b)
			return err
		}})
	}
	return arms
}

func TestCodesCoverFailureTable(t *testing.T) {
	t.Parallel()
	for _, arm := range failureArms(t) {
		t.Run(arm.name, func(t *testing.T) {
			t.Parallel()
			err := arm.call()
			if err == nil {
				t.Fatal("call succeeded, want a failure")
			}
			if !slices.ContainsFunc(timing.Codes, func(c code) bool {
				return errors.Is(err, c.Err)
			}) {
				t.Errorf("error %v is no entry of timing.Codes", err)
			}
		})
	}
}

// sentinels names every exported Err* var of the package; a new sentinel
// must be added here and to Codes or exempt.
var sentinels = map[string]error{
	"ErrTimingInvalid":     timing.ErrTimingInvalid,
	"ErrFormatUnsupported": timing.ErrFormatUnsupported,
}

// exempt lists sentinels deliberately left out of Codes, with the reason.
var exempt = map[string]string{}

// Go cannot enumerate package vars by reflect, so the source is parsed.
func TestEverySentinelCoded(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					if id.IsExported() && strings.HasPrefix(id.Name, "Err") {
						declared = append(declared, id.Name)
					}
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no exported Err* var; the parse is broken")
	}
	for _, name := range declared {
		v, ok := sentinels[name]
		if !ok {
			t.Errorf("%s is missing from this test's sentinels table", name)
			continue
		}
		_, isExempt := exempt[name]
		coded := slices.ContainsFunc(timing.Codes, func(c code) bool {
			return c.Err == v
		})
		if !coded && !isExempt {
			t.Errorf("%s is neither in timing.Codes nor exempt", name)
		}
	}
	for name := range sentinels {
		if !slices.Contains(declared, name) {
			t.Errorf("sentinels names %s, which the package does not declare", name)
		}
	}
}
