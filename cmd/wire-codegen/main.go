// Command wire-codegen generates TypeScript interfaces, decoders, and an SSE
// registry from Go wire types using reflect. Output replaces hand-written
// validators in internal/server/static-src/validators.ts.
//
// Run: go run ./cmd/wire-codegen   (from apps/subflux/)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"subflux/internal/api"
	"subflux/internal/server/activity"
	"subflux/internal/server/coverage"
	"subflux/internal/server/coveragehandlers"
	"subflux/internal/server/events"
)

const (
	tsUnknown      = "unknown"
	tsIdentityCast = "(v) => v as unknown"
)

// EnumDef defines a named string enum with its valid values.
type EnumDef struct{ Values []string }

// Enums maps Go type names to their enum values.
var Enums = map[string]EnumDef{
	"MediaType":      {Values: []string{"movie", "episode", "series"}},
	"Variant":        {Values: []string{"standard", "hi", "forced"}},
	"Role":           {Values: []string{"admin", "user"}},
	"ScoreTier":      {Values: []string{"excellent", "good", "acceptable", "minimal", "none"}},
	"NotifyLevel":    {Values: []string{"error", "success", "info"}},
	"ActivitySource": {Values: []string{"scheduled", "manual"}},
}

// enumTSName maps Go enum type names to their TS type name (for dedup/aliasing).
var enumTSName = map[string]string{}

// WireTypes is the set of Go struct types to generate TS for.
var WireTypes = []reflect.Type{
	reflect.TypeFor[coverage.TargetCoverage](),
	reflect.TypeFor[coveragehandlers.SeriesCoverage](),
	reflect.TypeFor[coveragehandlers.MovieCoverage](),
	reflect.TypeFor[api.SubtitleFileRow](),
	reflect.TypeFor[api.StateStatsResponse](),
	reflect.TypeFor[api.TimeoutStatus](),
	reflect.TypeFor[api.ProvidersResponse](),
	reflect.TypeFor[api.UserMeResponse](),
	reflect.TypeFor[api.SchemaField](),
	reflect.TypeFor[api.SchemaOption](),
	reflect.TypeFor[api.SchemaSection](),
	reflect.TypeFor[api.ProviderSchema](),
	reflect.TypeFor[api.StateEntry](),
	reflect.TypeFor[api.ScorePreviewResponse](),
	reflect.TypeFor[api.SearchTarget](),
	reflect.TypeFor[api.SearchTargetsResponse](),
	reflect.TypeFor[api.SetupStatusResponse](),
	reflect.TypeFor[api.LoginSuccessResponse](),
	reflect.TypeFor[api.WebAuthnSignalDataResponse](),
	reflect.TypeFor[api.PasskeyRegisteredResponse](),
	reflect.TypeFor[api.KeyGeneratedResponse](),
	reflect.TypeFor[events.CoverageEvent](),
	reflect.TypeFor[events.NotifyEvent](),
	reflect.TypeFor[events.ScanEvent](),
	reflect.TypeFor[activity.Entry](),
}

// SSERegEntry maps an SSE event type to a registered struct name.
type SSERegEntry struct {
	EventType string
	TypeName  string
}

// SSEEvents is the set of SSE events to register decoders for.
// NOTE: subflux's SSE handling is inline in events.ts (no registerSSEDecoder
// infrastructure). This registry is generated for future use but the
// registry.gen.ts will not be imported until events.ts is refactored.
var SSEEvents = []SSERegEntry{
	{EventType: "coverage", TypeName: "CoverageEvent"},
	{EventType: "notify", TypeName: "NotifyEvent"},
	{EventType: "scan:start", TypeName: "ScanEvent"},
	{EventType: "scan:done", TypeName: "ScanEvent"},
}

// typeByName indexes registered types by Go name for cross-references.
var typeByName = map[string]reflect.Type{}

// tsNameOverride maps Go type names to preferred TS names.
var tsNameOverride = map[string]string{
	"SubtitleFileRow":            "SubtitleEntry",
	"StateStatsResponse":         "Stats",
	"TimeoutStatus":              "ProviderStatus",
	"Entry":                      "ActivityEntry",
	"UserMeResponse":             "MeResponse",
	"ScorePreviewResponse":       "ScorePreview",
	"SearchTargetsResponse":      "SearchTargets",
	"SetupStatusResponse":        "SetupStatus",
	"LoginSuccessResponse":       "LoginSuccess",
	"WebAuthnSignalDataResponse": "SignalData",
	"PasskeyRegisteredResponse":  "PasskeyRegistered",
	"KeyGeneratedResponse":       "KeyGenerated",
	"TargetCoverage":             "CoverageTarget",
	"SeriesCoverage":             "SeriesItem",
	"MovieCoverage":              "MovieItem",
}

func tsName(goName string) string {
	if override, ok := tsNameOverride[goName]; ok {
		return override
	}
	return goName
}

func init() {
	for _, t := range WireTypes {
		typeByName[t.Name()] = t
	}
}

// fieldInfo holds parsed metadata for one struct field.
type fieldInfo struct {
	goType   reflect.Type
	wireName string
	optional bool
}

func parseFields(t reflect.Type) []fieldInfo {
	var fields []fieldInfo
	for sf := range t.Fields() {
		if sf.Anonymous {
			embedded := sf.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			fields = append(fields, parseFields(embedded)...)
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		wireName := parts[0]
		if wireName == "" {
			wireName = sf.Name
		}
		if wireName == "-" {
			continue
		}
		omitempty := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				omitempty = true
			}
		}
		optional := omitempty || sf.Type.Kind() == reflect.Pointer || sf.Type.Kind() == reflect.Map
		fields = append(fields, fieldInfo{wireName: wireName, goType: sf.Type, optional: optional})
	}
	return fields
}

// tsType returns the TypeScript type string for a Go reflect.Type.
func tsType(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return tsType(t.Elem())
	}
	if t.Name() != "" {
		if _, ok := Enums[t.Name()]; ok {
			return tsEnumName(t.Name())
		}
		if _, ok := typeByName[t.Name()]; ok {
			return tsName(t.Name())
		}
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return tsUnknown
	}
	if t == reflect.TypeFor[time.Time]() {
		return "string"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice:
		return tsType(t.Elem()) + "[]"
	case reflect.Map:
		return "Record<string, " + tsType(t.Elem()) + ">"
	case reflect.Interface:
		return tsUnknown
	case reflect.Struct:
		return tsUnknown
	}
	return tsUnknown
}

func tsEnumName(goName string) string {
	if override, ok := enumTSName[goName]; ok {
		return override
	}
	return goName
}

func decoderName(typeName string) string {
	return "decode" + tsName(typeName)
}

func pathName(typeName string) string {
	if override, ok := pathNameOverride[typeName]; ok {
		return override
	}
	var b strings.Builder
	runes := []rune(typeName)
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				if prev >= 'a' && prev <= 'z' {
					b.WriteByte('_')
				} else if prev >= 'A' && prev <= 'Z' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var pathNameOverride = map[string]string{}

func enumConstName(goTypeName string) string {
	name := tsEnumName(goTypeName)
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				if prev >= 'a' && prev <= 'z' {
					b.WriteByte('_')
				} else if prev >= 'A' && prev <= 'Z' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r)
		} else {
			b.WriteRune(r - 32)
		}
	}
	b.WriteString("S")
	return b.String()
}

func isPrimitive(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		return isPrimitive(t.Elem())
	}
	if t == reflect.TypeFor[time.Time]() {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func isEnum(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		return isEnum(t.Elem())
	}
	_, ok := Enums[t.Name()]
	return ok && t.Kind() == reflect.String
}

func isStruct(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		return isStruct(t.Elem())
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	_, ok := typeByName[t.Name()]
	return ok
}

func isRawMessage(t reflect.Type) bool {
	return t == reflect.TypeFor[json.RawMessage]()
}

func isInterface(t reflect.Type) bool {
	return t.Kind() == reflect.Interface
}

func primHelper(t reflect.Type, optional bool) string {
	if t.Kind() == reflect.Pointer {
		return primHelper(t.Elem(), optional)
	}
	if t == reflect.TypeFor[time.Time]() {
		if optional {
			return "optStr"
		}
		return "reqStr"
	}
	prefix := "req"
	if optional {
		prefix = "opt"
	}
	switch t.Kind() {
	case reflect.String:
		return prefix + "Str"
	case reflect.Bool:
		return prefix + "Bool"
	default:
		return prefix + "Num"
	}
}

func elemDecoderExpr(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if isStruct(t) {
		return decoderName(t.Name())
	}
	if t.Kind() == reflect.String {
		return "(v) => { if (typeof v !== \"string\") throw new TypeError(\"expected string\"); return v as string; }"
	}
	// Numeric types
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "(v) => { if (typeof v !== \"number\") throw new TypeError(\"expected number\"); return v as number; }"
	case reflect.Bool:
		return "(v) => { if (typeof v !== \"boolean\") throw new TypeError(\"expected boolean\"); return v as boolean; }"
	}
	if t.Kind() == reflect.Interface {
		return tsIdentityCast
	}
	if isRawMessage(t) {
		return tsIdentityCast
	}
	if t.Kind() == reflect.Map {
		return "(v) => asObject(v)"
	}
	if t.Name() != "" && isEnum(t) {
		return decoderName(t.Name())
	}
	return tsIdentityCast
}

// generateTypes writes types.gen.ts.
func generateTypes(w *strings.Builder) {
	w.WriteString("// CODE-GENERATED by cmd/wire-codegen, DO NOT EDIT.\n\n")

	enumNames := make([]string, 0, len(Enums))
	seenEnumTS := map[string]bool{}
	for name := range Enums {
		tn := tsEnumName(name)
		if seenEnumTS[tn] {
			continue
		}
		seenEnumTS[tn] = true
		enumNames = append(enumNames, name)
	}
	sort.Slice(enumNames, func(i, j int) bool { return tsEnumName(enumNames[i]) < tsEnumName(enumNames[j]) })
	for _, name := range enumNames {
		def := Enums[name]
		w.WriteString("export type " + tsEnumName(name) + " = ")
		for i, v := range def.Values {
			if i > 0 {
				w.WriteString(" | ")
			}
			w.WriteString("\"" + v + "\"")
		}
		w.WriteString(";\n\n")
	}

	names := make([]string, 0, len(WireTypes))
	for _, t := range WireTypes {
		names = append(names, t.Name())
	}
	sort.Slice(names, func(i, j int) bool { return tsName(names[i]) < tsName(names[j]) })
	for _, name := range names {
		t := typeByName[name]
		fields := parseFields(t)
		w.WriteString("export interface " + tsName(name) + " {\n")
		for _, f := range fields {
			ts := tsType(f.goType)
			if f.optional {
				w.WriteString("  " + f.wireName + "?: " + ts + ";\n")
			} else {
				w.WriteString("  " + f.wireName + ": " + ts + ";\n")
			}
		}
		w.WriteString("}\n\n")
	}
}

// generateDecoders writes decoders.gen.ts.
//
//nolint:gocyclo // type-switch over reflect kinds is inherently branchy
func generateDecoders(w *strings.Builder) {
	var bodies strings.Builder
	goNames := make([]string, 0, len(WireTypes))
	for _, t := range WireTypes {
		goNames = append(goNames, t.Name())
	}
	sort.Slice(goNames, func(i, j int) bool { return tsName(goNames[i]) < tsName(goNames[j]) })
	for _, name := range goNames {
		t := typeByName[name]
		emitDecoder(&bodies, name, t)
	}
	body := bodies.String()

	w.WriteString("// CODE-GENERATED by cmd/wire-codegen, DO NOT EDIT.\n\n")
	allHelpers := []string{
		"asObject", "asArray", "reqStr", "reqNum", "reqBool",
		"optStr", "optNum", "optBool", "reqOneOf",
		"decodeArray", "decodeRecord",
	}
	usedHelpers := []string{}
	for _, h := range allHelpers {
		if isIdentReferenced(body, h) {
			usedHelpers = append(usedHelpers, h)
		}
	}
	w.WriteString("import { ")
	if len(usedHelpers) > 0 {
		w.WriteString(strings.Join(usedHelpers, ", "))
		w.WriteString(", ")
	}
	w.WriteString("type Decoder } from \"../validators.js\";\n")

	candidateNames := make([]string, 0, len(WireTypes))
	for _, t := range WireTypes {
		candidateNames = append(candidateNames, tsName(t.Name()))
	}
	enumSeen := map[string]bool{}
	for name := range Enums {
		tn := tsEnumName(name)
		if !enumSeen[tn] {
			enumSeen[tn] = true
			candidateNames = append(candidateNames, tn)
		}
	}
	usedSet := map[string]bool{}
	for _, n := range candidateNames {
		if isIdentReferenced(body, n) {
			usedSet[n] = true
		}
	}
	used := make([]string, 0, len(usedSet))
	for n := range usedSet {
		used = append(used, n)
	}
	sort.Strings(used)
	if len(used) > 0 {
		w.WriteString("import type { ")
		w.WriteString(strings.Join(used, ", "))
		w.WriteString(" } from \"./types.gen.js\";\n")
	}
	w.WriteString("\n")

	emitted := map[string]bool{}
	for _, name := range enumNamesSlice(Enums) {
		constN := enumConstName(name)
		if emitted[constN] {
			continue
		}
		if !isIdentReferenced(body, constN) {
			continue
		}
		emitted[constN] = true
		def := Enums[name]
		w.WriteString("const " + constN + " = [")
		for i, v := range def.Values {
			if i > 0 {
				w.WriteString(", ")
			}
			w.WriteString("\"" + v + "\"")
		}
		w.WriteString("] as const;\n")
	}
	if len(emitted) > 0 {
		w.WriteString("\n")
	}

	w.WriteString(body)
}

func isIdentReferenced(body, ident string) bool {
	for i := 0; i < len(body); {
		j := strings.Index(body[i:], ident)
		if j < 0 {
			return false
		}
		j += i
		if j > 0 {
			c := body[j-1]
			if isIdentChar(c) {
				i = j + len(ident)
				continue
			}
		}
		end := j + len(ident)
		if end < len(body) {
			c := body[end]
			if isIdentChar(c) {
				i = end
				continue
			}
		}
		return true
	}
	return false
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '$'
}

func enumNamesSlice(m map[string]EnumDef) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func emitDecoder(w *strings.Builder, name string, t reflect.Type) {
	fields := parseFields(t)
	tn := tsName(name)
	path := "$." + pathName(tn)
	w.WriteString("export const " + decoderName(name) + ": Decoder<" + tn + "> = (v) => {\n")
	w.WriteString("  const o = asObject(v, \"" + path + "\");\n")

	var reqFields, optFields []fieldInfo
	for _, f := range fields {
		if f.optional {
			optFields = append(optFields, f)
		} else {
			reqFields = append(reqFields, f)
		}
	}

	if len(reqFields) > 0 || len(optFields) > 0 {
		w.WriteString("  const out: " + tn + " = {\n")
		for _, f := range reqFields {
			w.WriteString("    " + f.wireName + ": " + reqExpr(f, path) + ",\n")
		}
		w.WriteString("  };\n")
	} else {
		w.WriteString("  const out: " + tn + " = {};\n")
	}

	for _, f := range optFields {
		emitOptionalField(w, f, path)
	}

	w.WriteString("  return out;\n")
	w.WriteString("};\n\n")
}

func reqExpr(f fieldInfo, path string) string {
	t := f.goType
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if isRawMessage(t) {
		return "o[\"" + f.wireName + "\"] as unknown"
	}
	if isInterface(t) {
		return "o[\"" + f.wireName + "\"] as unknown"
	}
	if isEnum(t) {
		return "reqOneOf(o, \"" + f.wireName + "\", " + enumConstName(t.Name()) + ", \"" + path + "\")"
	}
	if isPrimitive(t) {
		return primHelper(t, false) + "(o, \"" + f.wireName + "\", \"" + path + "\")"
	}
	if isStruct(t) {
		return decoderName(t.Name()) + "(o[\"" + f.wireName + "\"])"
	}
	if t.Kind() == reflect.Slice {
		elem := t.Elem()
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		return "decodeArray(o[\"" + f.wireName + "\"], " + elemDecoderExpr(elem) + ", \"" + path + "." + f.wireName + "\")"
	}
	if t.Kind() == reflect.Map {
		valType := t.Elem()
		return "decodeRecord(o[\"" + f.wireName + "\"], " + elemDecoderExpr(valType) + ", \"" + path + "." + f.wireName + "\")"
	}
	return "o[\"" + f.wireName + "\"] as unknown"
}

func emitOptionalField(w *strings.Builder, f fieldInfo, path string) {
	t := f.goType
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if isRawMessage(t) {
		w.WriteString("  if (o[\"" + f.wireName + "\"] !== undefined) out." + f.wireName + " = o[\"" + f.wireName + "\"] as unknown;\n")
		return
	}
	if isInterface(t) {
		w.WriteString("  if (o[\"" + f.wireName + "\"] !== undefined) out." + f.wireName + " = o[\"" + f.wireName + "\"] as unknown;\n")
		return
	}
	if isEnum(t) {
		w.WriteString("  if (o[\"" + f.wireName + "\"] !== undefined) out." + f.wireName + " = reqOneOf(o, \"" + f.wireName + "\", " + enumConstName(t.Name()) + ", \"" + path + "\");\n")
		return
	}
	if isPrimitive(t) {
		helper := primHelper(t, true)
		varName := sanitizeVarName(f.wireName)
		w.WriteString("  const " + varName + " = " + helper + "(o, \"" + f.wireName + "\", \"" + path + "\");\n")
		w.WriteString("  if (" + varName + " !== undefined) out." + f.wireName + " = " + varName + ";\n")
		return
	}
	if isStruct(t) {
		w.WriteString("  if (o[\"" + f.wireName + "\"] !== undefined) out." + f.wireName + " = " + decoderName(t.Name()) + "(o[\"" + f.wireName + "\"]);\n")
		return
	}
	if t.Kind() == reflect.Slice {
		elem := t.Elem()
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		w.WriteString("  if (o[\"" + f.wireName + "\"] !== undefined) out." + f.wireName + " = decodeArray(o[\"" + f.wireName + "\"], " + elemDecoderExpr(elem) + ", \"" + path + "." + f.wireName + "\");\n")
		return
	}
	if t.Kind() == reflect.Map {
		valType := t.Elem()
		w.WriteString("  if (o[\"" + f.wireName + "\"] !== undefined) out." + f.wireName + " = decodeRecord(o[\"" + f.wireName + "\"], " + elemDecoderExpr(valType) + ", \"" + path + "." + f.wireName + "\");\n")
		return
	}
	w.WriteString("  if (o[\"" + f.wireName + "\"] !== undefined) out." + f.wireName + " = o[\"" + f.wireName + "\"] as unknown;\n")
}

func sanitizeVarName(wireName string) string {
	parts := strings.Split(wireName, "_")
	var b strings.Builder
	for i, p := range parts {
		if i == 0 {
			b.WriteString(p)
		} else if p != "" {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])

		}
	}
	s := b.String()
	switch s {
	case "o", "out", "v", "private", "public", "protected", "class",
		"return", "delete", "default", "export", "import", "new", "this":
		return s + "Val"
	}
	return s
}

// generateRegistry writes registry.gen.ts.
// NOTE: subflux does not currently have registerSSEDecoder infrastructure.
// This file is generated for future use when events.ts is refactored.
func generateRegistry(w *strings.Builder) {
	w.WriteString("// CODE-GENERATED by cmd/wire-codegen, DO NOT EDIT.\n")
	w.WriteString("//\n")
	w.WriteString("// NOTE: subflux SSE handling is inline in events.ts.\n")
	w.WriteString("// This registry is generated for future use. Import and call\n")
	w.WriteString("// registerAllSSEDecoders() once events.ts adopts the pattern.\n\n")

	decoderImports := make([]string, 0)
	seen := map[string]bool{}
	for _, e := range SSEEvents {
		dn := decoderName(e.TypeName)
		if !seen[dn] {
			seen[dn] = true
			decoderImports = append(decoderImports, dn)
		}
	}
	sort.Strings(decoderImports)
	w.WriteString("import { " + strings.Join(decoderImports, ", ") + " } from \"./decoders.gen.js\";\n")
	w.WriteString("import type { Decoder } from \"../validators.js\";\n\n")

	w.WriteString("const registry = new Map<string, Decoder<unknown>>();\n\n")

	w.WriteString("export function registerAllSSEDecoders(): void {\n")
	for _, e := range SSEEvents {
		w.WriteString("  registry.set(\"" + e.EventType + "\", " + decoderName(e.TypeName) + " as Decoder<unknown>);\n")
	}
	w.WriteString("}\n\n")

	w.WriteString("export function getSSEDecoder(eventType: string): Decoder<unknown> | undefined {\n")
	w.WriteString("  return registry.get(eventType);\n")
	w.WriteString("}\n")
}

func main() {
	outDir := filepath.Join("internal", "server", "static-src", "wire")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", outDir, err)
		os.Exit(1)
	}

	var typesBuf strings.Builder
	generateTypes(&typesBuf)
	if err := os.WriteFile(filepath.Join(outDir, "types.gen.ts"), []byte(typesBuf.String()), 0o644); err != nil { //nolint:gosec // G306: generated source file, not secrets
		fmt.Fprintf(os.Stderr, "write types.gen.ts: %v\n", err)
		os.Exit(1)
	}

	var decodersBuf strings.Builder
	generateDecoders(&decodersBuf)
	if err := os.WriteFile(filepath.Join(outDir, "decoders.gen.ts"), []byte(decodersBuf.String()), 0o644); err != nil { //nolint:gosec // G306: generated source file, not secrets
		fmt.Fprintf(os.Stderr, "write decoders.gen.ts: %v\n", err)
		os.Exit(1)
	}

	var registryBuf strings.Builder
	generateRegistry(&registryBuf)
	if err := os.WriteFile(filepath.Join(outDir, "registry.gen.ts"), []byte(registryBuf.String()), 0o644); err != nil { //nolint:gosec // G306: generated source file, not secrets
		fmt.Fprintf(os.Stderr, "write registry.gen.ts: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("wire-codegen: generated internal/server/static-src/wire/{types,decoders,registry}.gen.ts")
}
