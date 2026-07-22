//go:build functional

package functional

// TestJSONHelpersOracle pins every typed JSON helper in
// json_helpers_test.go against testdata/jq-oracle.json: a 761-case
// synthetic corpus (realistic wire-shaped bodies plus adversarial
// variants) whose expected outputs are the byte-for-byte behavior of the
// retired in-suite jq interpreter — itself proven equivalent to the bash
// suite's real-jq output on the live CI sections — cross-checked against
// real jq 1.8.1 (the 61 deliberate divergences, all on inputs the live
// API cannot produce, are documented in the generator's divergences.md).
//
// The test is offline: it never dials a server (like
// TestFunctionalSectionList). Run it with:
//
//	go test -tags functional -count=1 -run TestJSONHelpersOracle ./tests/functional/
//
// Regenerating the oracle: the corpus/golden generator lives outside the
// repo (workspace _rewrite/simplify/gen-oracle.py: corpus -> realjq ->
// merge). For a deliberate contract change it is fine to hand-edit the
// affected cases' "want" instead; each case carries the original jq
// program, render mode, and --arg bindings it mirrors.

import (
	"encoding/json"
	"os"
	"testing"
)

type oracleCase struct {
	ID     string            `json:"id"`
	Prog   string            `json:"prog"`
	Mode   string            `json:"mode"`
	Vars   map[string]string `json:"vars"`
	Helper string            `json:"helper"`
	Args   []string          `json:"args"`
	Body   string            `json:"body"`
	Want   string            `json:"want"`
}

// runHelper dispatches an oracle case to the helper under test.
func runHelper(t *testing.T, c oracleCase) string {
	t.Helper()
	arg := func(i int) string {
		if i >= len(c.Args) {
			t.Fatalf("%s: helper %s wants arg %d, have %v", c.ID, c.Helper, i, c.Args)
		}
		return c.Args[i]
	}
	switch c.Helper {
	case "fieldRaw":
		return fieldRaw(c.Body, arg(0))
	case "fieldJSON":
		return fieldJSON(c.Body, arg(0))
	case "fieldRawOrEmpty":
		return fieldRawOrEmpty(c.Body, arg(0))
	case "fieldJSONOrEmpty":
		return fieldJSONOrEmpty(c.Body, arg(0))
	case "fieldRawOrFalse":
		return fieldRawOrFalse(c.Body, arg(0))
	case "fieldJSONOrFalse":
		return fieldJSONOrFalse(c.Body, arg(0))
	case "fieldJSONOrZero":
		return fieldJSONOrZero(c.Body, arg(0))
	case "fieldNotNull":
		return fieldNotNull(c.Body, arg(0))
	case "lengthAt":
		return lengthAt(c.Body, arg(0))
	case "resultsLen":
		return resultsLen(c.Body)
	case "countFieldEq":
		return countFieldEq(c.Body, arg(0), arg(1))
	case "schemaProviderCount":
		return schemaProviderCount(c.Body, arg(0))
	case "seriesPick":
		return seriesPick(c.Body)
	case "activityRow":
		return activityRow(c.Body, arg(0))
	case "runningLen":
		return runningLen(c.Body, arg(0))
	case "providerBreakdown":
		return providerBreakdown(c.Body)
	default:
		t.Fatalf("%s: unknown helper %q", c.ID, c.Helper)
		return ""
	}
}

func TestJSONHelpersOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/jq-oracle.json")
	if err != nil {
		t.Fatalf("read oracle: %v", err)
	}
	var oracle struct {
		Cases []oracleCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatalf("decode oracle: %v", err)
	}
	if len(oracle.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	helpers := make(map[string]int)
	for _, c := range oracle.Cases {
		if got := runHelper(t, c); got != c.Want {
			t.Errorf("%s: %s(%v)\n  jq equivalent: %s (mode %s, vars %v)\n  body: %q\n  got:  %q\n  want: %q",
				c.ID, c.Helper, c.Args, c.Prog, c.Mode, c.Vars, c.Body, got, c.Want)
		}
		helpers[c.Helper]++
	}
	t.Logf("oracle: %d cases across %d helpers", len(oracle.Cases), len(helpers))
}
