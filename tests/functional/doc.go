//go:build functional

// Package functional contains the black-box functional test suite that
// drives a live subflux instance over its HTTP API. It is the Go port of
// the retired tests/functional/run.sh: 26 ordered sections sharing one
// suite state, with pass/fail/skip counters and per-assertion output lines
// reproduced verbatim (equivalence was proven on the sorted PASS/FAIL/SKIP
// assertion lines of the 12 CI sections; incidental bash stdout noise such
// as uncaptured response bodies is deliberately not reproduced, and ANSI
// colors are dropped).
//
// Response inspection uses the typed helpers in json_helpers_test.go, each
// mirroring one exact jq program run.sh ran (byte-for-byte, including
// error-collapse-to-"" and jq's rendering rules). Their contract is pinned
// offline by TestJSONHelpersOracle against testdata/jq-oracle.json.
//
// The package is excluded from regular builds and test runs by the
// "functional" build tag (wildcard ./... patterns skip fully-excluded
// packages). Run it against a live instance with:
//
//	go test -tags functional -run TestFunctional -count=1 ./tests/functional/
//
// Configuration comes from the same environment variables as run.sh:
// SUBFLUX_URL (default http://192.0.2.77:8374) and SECTION (default "all";
// one section name to run only that section). Individual sections can also
// be selected with -run 'TestFunctional/<name>'.
package functional
