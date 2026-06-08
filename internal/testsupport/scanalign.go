// Package testsupport — scanner-alignment helpers.
//
// The store package consolidates SQL column lists and Scan-argument lists
// in tableScanner-like structs (each scanner pairs a `columns` string with
// a `scan` or `scanInto` function). This file provides reusable helpers
// that detect drift between those two halves at test time.
//
// The bug class targeted: a column added to the SELECT list without a
// matching pointer in the Scan call (or vice versa) panics at runtime
// when the query is executed. The helpers below catch this in `go test`
// instead.
//
// Borrows sqlc's safety property (compile-time scan-order validation)
// without taking the codegen tool. Coverage is partial: the test catches
// add/remove drift definitively and reorder drift when the test author
// supplies an expected-field list.
package testsupport

import (
	"reflect"
	"strings"
)

// ScanRecorder satisfies the `interface{ Scan(...any) error }` shape used
// by row scanners and captures the pointer arguments passed to Scan in
// the order they were passed.
type ScanRecorder struct {
	Args []any
}

// Scan records args and returns nil. Pass &ScanRecorder{} as the row to
// any scanInto / scan function to capture its argument order.
func (r *ScanRecorder) Scan(args ...any) error {
	r.Args = append(r.Args, args...)
	return nil
}

// SplitColumns parses a `tableScanner.columns` string into a slice of
// trimmed column names. Handles multi-line column lists with tabs,
// newlines, and surrounding whitespace.
//
// Example:
//
//	SplitColumns("id, user_id,\n\ttoken_hash, created_at")
//	-> ["id", "user_id", "token_hash", "created_at"]
func SplitColumns(s string) []string {
	cleaned := strings.NewReplacer("\n", " ", "\t", " ").Replace(s)
	parts := strings.Split(cleaned, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// FieldNameAt reports the name of the struct field within `sample` that
// holds the address `ptr`. Returns "" if `ptr` is outside the memory
// range of `sample` (a local variable passed to Scan instead of a
// struct field, e.g. `var manual int; row.Scan(..., &manual, ...)`).
//
// Both arguments must be pointers; sample must be a pointer to a struct.
// The check is offset-based: each pointer is compared against the
// addresses of every exported and unexported field. Embedded struct
// fields are not unwrapped; pass the embedded type directly if needed.
func FieldNameAt(sample, ptr any) string {
	sampleVal := reflect.ValueOf(sample)
	if sampleVal.Kind() != reflect.Pointer || sampleVal.IsNil() {
		return ""
	}
	sampleVal = sampleVal.Elem()
	if sampleVal.Kind() != reflect.Struct {
		return ""
	}

	ptrVal := reflect.ValueOf(ptr)
	if ptrVal.Kind() != reflect.Pointer || ptrVal.IsNil() {
		return ""
	}

	sampleAddr := sampleVal.UnsafeAddr()
	sampleSize := sampleVal.Type().Size()
	ptrAddr := ptrVal.Pointer()

	if ptrAddr < sampleAddr || ptrAddr >= sampleAddr+sampleSize {
		return ""
	}

	offset := ptrAddr - sampleAddr
	for i := range sampleVal.NumField() {
		field := sampleVal.Type().Field(i)
		if field.Offset == offset {
			return field.Name
		}
	}
	return ""
}

// AlignmentResult captures the outcome of an Aligned check. The test
// author decides which assertions to run against it: count match alone
// catches add/remove drift; comparing Fields against an expected slice
// catches reorder drift.
type AlignmentResult struct {
	// Columns is the parsed column list from the scanner's columns string.
	Columns []string
	// ScanArgs is the slice of pointer args captured by ScanRecorder.
	ScanArgs []any
	// Fields contains the resolved field name for each ScanArg, or ""
	// for args that point outside the sample struct (local variables).
	Fields []string
}
