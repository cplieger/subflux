//go:build wiregen_dogfood

// Dogfood for wiregen's opt-in GenerateValidators() starter emitter (wiregen
// D9). This test is gated behind the `wiregen_dogfood` build tag so it is inert
// in the normal `go test ./...` run: it exercises an API (GenerateValidators)
// that only exists in a local/unreleased wiregen, so it is run explicitly via
//
//	GOWORK=<tmp>.work go test -tags wiregen_dogfood ./cmd/wire-codegen
//
// with a workspace that points github.com/cplieger/wiregen at the local
// working copy. It runs the starter emit path in real codegen context and
// verifies that subflux's hand-owned validators.ts satisfies the exact same
// "Validators contract" the generated decoders.gen.ts import by name. It never
// writes or mutates the owned validators.ts — it only reads it.
package main

import (
	"os"
	"strings"
	"testing"

	"github.com/cplieger/wiregen"
)

// ownedValidatorsPath is subflux's hand-authored validators module, relative to
// this package directory (cmd/wire-codegen).
const ownedValidatorsPath = "../../internal/server/static-src/validators.ts"

// validatorsContract is the exact set the starter MUST export and the owned
// copy MUST satisfy: the 11 helper functions the generated decoders reference
// by name (a referenced subset is imported, so the full set must exist).
var validatorsContract = []string{
	"asObject", "asArray",
	"reqStr", "reqNum", "reqBool",
	"optStr", "optNum", "optBool",
	"reqOneOf",
	"decodeArray", "decodeRecord",
}

// TestDogfoodValidatorsStarterEmitPath runs the opt-in emit path and asserts the
// starter exports the full contract plus Decoder<T>, and carries no
// generated-file banner (it is consumer-owned, never regenerated).
func TestDogfoodValidatorsStarterEmitPath(t *testing.T) {
	starter := wiregen.NewRegistry().GenerateValidators()

	for _, name := range validatorsContract {
		if !strings.Contains(starter, "export function "+name) {
			t.Errorf("starter missing exported helper %q", name)
		}
	}
	if !strings.Contains(starter, "export type Decoder<T> = (v: unknown) => T;") {
		t.Errorf("starter missing `export type Decoder<T> = (v: unknown) => T;`")
	}
	for _, forbidden := range []string{"DO NOT EDIT", "CODE-GENERATED"} {
		if strings.Contains(starter, forbidden) {
			t.Errorf("starter must not contain %q (it is consumer-owned, not generated)", forbidden)
		}
	}
}

// TestDogfoodOwnedValidatorsSatisfiesContract verifies subflux's owned
// validators.ts exports every helper the starter guarantees and the generated
// decoders import — proving the owned copy satisfies the contract and was not
// clobbered. Read-only.
func TestDogfoodOwnedValidatorsSatisfiesContract(t *testing.T) {
	owned, err := os.ReadFile(ownedValidatorsPath)
	if err != nil {
		t.Fatalf("read owned validators.ts: %v", err)
	}
	body := string(owned)

	for _, name := range validatorsContract {
		if !strings.Contains(body, "export function "+name) {
			t.Errorf("owned validators.ts missing exported helper %q", name)
		}
	}
	if !strings.Contains(body, "export type Decoder<T> = (v: unknown) => T;") {
		t.Errorf("owned validators.ts missing `export type Decoder<T> = (v: unknown) => T;`")
	}
	// The generated import is a referenced subset; the owned copy (like the
	// starter) must be a superset that exports all 11.
	for _, name := range validatorsContract {
		if !strings.Contains(body, "export function "+name) {
			t.Errorf("owned validators.ts is not a superset of the contract; missing %q", name)
		}
	}
}
