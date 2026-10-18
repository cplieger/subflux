package formschema

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestKindText(t *testing.T) {
	want := map[Kind]string{String: "string", Secret: "secret", Bool: "bool", Int: "int", DurationMs: "duration_ms", Enum: "enum", StringList: "string_list"}
	for k, name := range want {
		b, err := json.Marshal(k)
		if err != nil || string(b) != `"`+name+`"` {
			t.Errorf("Marshal(%d) = %s, %v; want %q", k, b, err, name)
		}
		var back Kind
		if err := json.Unmarshal(b, &back); err != nil || back != k {
			t.Errorf("Unmarshal(%s) = %d, %v; want %d", b, back, err, k)
		}
	}
	if _, err := json.Marshal(Kind(0)); err == nil {
		t.Error("the zero Kind marshalled")
	}
	for _, bad := range []string{`"String"`, `"duration"`, `""`, `"stringlist"`} {
		var k Kind
		if err := json.Unmarshal([]byte(bad), &k); err == nil {
			t.Errorf("Unmarshal(%s) accepted as %d", bad, k)
		}
	}
}

func TestDecodeKeepsScalarText(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "api_key", Kind: Secret}, {Key: "name", Kind: String}, {Key: "n", Kind: Int}}}
	for _, text := range []string{"0123", "1e3", "0x1F", "true", "0.10", "~x"} {
		t.Run(text, func(t *testing.T) {
			vals, errs := s.Decode(section(t, "api_key: "+text+"\nname: "+text+"\n"))
			if len(errs) != 0 {
				t.Fatalf("errors: %v", errs)
			}
			if got, _ := vals.Secret("api_key"); got != text {
				t.Errorf("Secret = %q, want %q", got, text)
			}
			if got, _ := vals.String("name"); got != text {
				t.Errorf("String = %q, want %q", got, text)
			}
		})
	}
	vals, errs := s.Decode(section(t, `n: "42"`))
	if got, ok := vals.Int("n"); len(errs) != 0 || !ok || got != 42 {
		t.Errorf(`Int from "42" = %d, %v, %v; want 42`, got, ok, errs)
	}
}

func TestDecodeAppliesDefaults(t *testing.T) {
	s := Schema{Fields: []Field{
		{Key: "enabled", Kind: Bool, Default: "false"},
		{Key: "frequency", Kind: DurationMs, Default: "24h"},
		{Key: "copies", Kind: Int, Default: "7", Min: 1, Max: 1000},
		{Key: "format", Kind: Enum, Enum: []string{"json", "text"}, Default: "json"},
		{Key: "path", Kind: String},
	}}
	vals, errs := s.Decode(section(t, "enabled: true\n"))
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if b, _ := vals.Bool("enabled"); !b {
		t.Error("enabled lost its written value")
	}
	if d, _ := vals.Duration("frequency"); d != 24*time.Hour {
		t.Errorf("frequency = %v, want the 24h default", d)
	}
	if n, _ := vals.Int("copies"); n != 7 {
		t.Errorf("copies = %d, want the default 7", n)
	}
	if f, _ := vals.String("format"); f != "json" {
		t.Errorf("format = %q, want the default json", f)
	}
	if _, ok := vals.String("path"); ok {
		t.Error("an absent key with no default has a value")
	}
	vals, errs = s.Decode(nil)
	if n, _ := vals.Int("copies"); len(errs) != 0 || n != 7 {
		t.Errorf("absent section: copies = %d, errors %v; want the default", n, errs)
	}
	bad := Schema{Fields: []Field{{Key: "n", Kind: Int, Default: "x"}, {Key: "l", Kind: StringList, Default: "a"}}}
	if _, errs := bad.Decode(nil); !slices.Equal(errPaths(errs), []string{"n", "l"}) {
		t.Errorf("invalid defaults: errors %v, want one per field", errs)
	}
}

func TestDecodeRequired(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "url", Kind: String, Required: true}, {Key: "hosts", Kind: StringList, Required: true}}}
	_, errs := s.Decode(section(t, "url: ''\nhosts: []\n"))
	if !slices.Equal(errPaths(errs), []string{"url", "hosts"}) {
		t.Errorf("empty required values: errors %v", errs)
	}
	_, errs = s.Decode(nil)
	if !slices.Equal(errPaths(errs), []string{"url", "hosts"}) {
		t.Errorf("absent required keys: errors %v", errs)
	}
}

func TestDecodeRefusesUnknownKey(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "a", Kind: String}}}
	vals, errs := s.Decode(section(t, "a: x\nb: y\na: z\n"))
	if !slices.Equal(errs, []FieldError{{"b", "unknown key"}, {"a", "key is set twice"}}) {
		t.Errorf("errors = %v", errs)
	}
	if got, _ := vals.String("a"); got != "x" {
		t.Errorf("a = %q, want the first value", got)
	}
	if _, errs := s.Decode(section(t, "- a\n")); !slices.Equal(errs, []FieldError{{"", "must be a mapping"}}) {
		t.Errorf("a sequence section: errors %v", errs)
	}
	if _, errs := s.Decode(section(t, "? [k]\n: v\n")); len(errs) != 1 || errs[0].Path != "" {
		t.Errorf("a non-scalar key: errors %v", errs)
	}
}

func TestDecodeBounds(t *testing.T) {
	s := Schema{Fields: []Field{
		{Key: "n", Kind: Int, Min: 1, Max: 10},
		{Key: "neg", Kind: Int, Min: -5, Max: -1},
		{Key: "free", Kind: Int},
		{Key: "level", Kind: Enum, Enum: []string{"info", "debug"}},
		{Key: "on", Kind: Bool},
		{Key: "list", Kind: StringList},
	}}
	cases := []struct {
		src  string
		path string
	}{
		{"n: 0", "n"},
		{"n: 11", "n"},
		{"neg: 0", "neg"},
		{"neg: -6", "neg"},
		{"n: 1.5", "n"},
		{"n: [1]", "n"},
		{"level: warn", "level"},
		{"on: yes", "on"},
		{"list: a", "list"},
		{"list: [a, ~]", "list[1]"},
		{"list: [a, {b: c}]", "list[1]"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			if _, errs := s.Decode(section(t, c.src)); !slices.Equal(errPaths(errs), []string{c.path}) {
				t.Errorf("errors = %v, want one at %s", errs, c.path)
			}
		})
	}
	vals, errs := s.Decode(section(t, "n: 10\nneg: -5\nfree: -900\nlevel: debug\non: FALSE\nlist: [a, '', b]\n"))
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if n, _ := vals.Int("free"); n != -900 {
		t.Errorf("unbounded int = %d", n)
	}
	if l, _ := vals.StringList("list"); !slices.Equal(l, []string{"a", "", "b"}) {
		t.Errorf("list = %q", l)
	}
	if on, ok := vals.Bool("on"); on || !ok {
		t.Errorf("on = %v, %v; want false, true", on, ok)
	}
}

func TestDecodeDurationWholeMilliseconds(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "d", Kind: DurationMs, Min: 1000, Max: 3_600_000}}}
	for src, want := range map[string]time.Duration{"d: 1s": time.Second, "d: 1500ms": 1500 * time.Millisecond, "d: 1h": time.Hour} {
		vals, errs := s.Decode(section(t, src))
		if d, _ := vals.Duration("d"); len(errs) != 0 || d != want {
			t.Errorf("%s: %v, %v; want %v", src, d, errs, want)
		}
	}
	for _, src := range []string{"d: 999ms", "d: 2h", "d: 5", "d: soon"} {
		if _, errs := s.Decode(section(t, src)); len(errs) != 1 {
			t.Errorf("%s: errors %v, want one", src, errs)
		}
	}
	free := Schema{Fields: []Field{{Key: "d", Kind: DurationMs}}}
	if _, errs := free.Decode(section(t, "d: 1500us")); !slices.Equal(errs, []FieldError{{"d", "must be whole milliseconds"}}) {
		t.Errorf("1500us: errors %v, want whole milliseconds refused", errs)
	}
}

func TestDecodeNullIsAbsent(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "a", Kind: Int, Default: "3"}, {Key: "b", Kind: String}, {Key: "c", Kind: String, Required: true}}}
	vals, errs := s.Decode(section(t, "a: ~\nb: null\nc:\n"))
	if !slices.Equal(errs, []FieldError{{"c", "required"}}) {
		t.Errorf("errors = %v, want c required", errs)
	}
	if n, _ := vals.Int("a"); n != 3 {
		t.Errorf("a null value did not take the default: %d", n)
	}
	if _, ok := vals.String("b"); ok {
		t.Error("a null string has a value")
	}
	if vals, errs := s.Decode(section(t, "b: 'null'\nc: x")); len(errs) != 0 {
		t.Errorf("quoted null: %v", errs)
	} else if b, _ := vals.String("b"); b != "null" {
		t.Errorf("quoted null read as %q", b)
	}
}

func TestDecodeRefusesSharedNodes(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "a", Kind: String}, {Key: "key", Kind: Secret}, {Key: "l", Kind: StringList}}}
	m := section(t, "a: &x s3cret\nkey: *x\nl: [&y b, *y]\n")
	_, errs := s.Decode(m)
	if !slices.Equal(errPaths(errs), []string{"a", "key", "l"}) {
		t.Errorf("errors = %v, want each shared value refused", errs)
	}
	if tag := valueNode(m, "a").Tag; tag == PlainTag {
		t.Error("an anchored scalar was marked, unmasking its alias")
	}
}

func TestValuesAnswerOnlyTheirKind(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "k", Kind: Secret}, {Key: "l", Kind: StringList}}}
	vals, _ := s.Decode(section(t, "k: x\nl: [a]\n"))
	if _, ok := vals.String("k"); ok {
		t.Error("String answered a Secret field")
	}
	l, _ := vals.StringList("l")
	l[0] = "changed"
	if again, _ := vals.StringList("l"); again[0] != "a" {
		t.Error("StringList shares its backing array")
	}
}

func TestPrefixMakesPathsAbsolute(t *testing.T) {
	got := Prefix("server", []FieldError{{"allowed_hosts[0]", "m"}, {"", "root"}, {"[2].id", "x"}})
	want := []FieldError{{"server.allowed_hosts[0]", "m"}, {"server", "root"}, {"server[2].id", "x"}}
	if !slices.Equal(got, want) {
		t.Errorf("Prefix = %v, want %v", got, want)
	}
	if got := Prefix("", []FieldError{{"a", "m"}}); got[0].Path != "a" {
		t.Errorf("empty prefix: %v", got)
	}
}
