package formschema

import (
	"slices"
	"testing"
	"time"
)

func TestValuesReadTypedValue(t *testing.T) {
	s := Schema{Fields: []Field{
		{Key: "s", Kind: String},
		{Key: "e", Kind: Enum, Enum: []string{"a", "b"}},
		{Key: "k", Kind: Secret},
		{Key: "on", Kind: Bool},
		{Key: "n", Kind: Int},
		{Key: "d", Kind: DurationMs},
		{Key: "l", Kind: StringList},
		{Key: "s0", Kind: String},
		{Key: "on0", Kind: Bool},
		{Key: "n0", Kind: Int},
		{Key: "d0", Kind: DurationMs},
		{Key: "l0", Kind: StringList},
		{Key: "nd", Kind: Int, Default: "9"},
		{Key: "dd", Kind: DurationMs, Default: "3s"},
		{Key: "bad", Kind: Int},
	}}
	vals, errs := s.Decode(section(t, "s: x\ne: b\nk: key\non: true\nn: 4\nd: 2s\nl: [a, b]\nbad: zero\n"))
	if !slices.Equal(errPaths(errs), []string{"bad"}) {
		t.Fatalf("errors = %v, want only bad", errs)
	}
	zero, _ := Schema{Fields: []Field{{Key: "z"}}}.Decode(section(t, "z: x\n"))
	t.Run("present", func(t *testing.T) {
		if vals.String("s") != "x" || vals.String("e") != "b" || vals.Secret("k") != "key" || !vals.Bool("on") ||
			vals.Int("n") != 4 || vals.Duration("d") != 2*time.Second || !slices.Equal(vals.StringList("l"), []string{"a", "b"}) {
			t.Errorf("got %v", vals)
		}
	})
	t.Run("absent reads the zero value", func(t *testing.T) {
		if vals.String("s0") != "" || vals.Bool("on0") || vals.Int("n0") != 0 || vals.Duration("d0") != 0 || vals.StringList("l0") != nil {
			t.Errorf("got %v", vals)
		}
	})
	t.Run("a refused field reads the zero value", func(t *testing.T) {
		if n := vals.Int("bad"); n != 0 {
			t.Errorf("bad = %d", n)
		}
	})
	t.Run("absent reads the default", func(t *testing.T) {
		if vals.Int("nd") != 9 || vals.Duration("dd") != 3*time.Second {
			t.Errorf("got %d, %v", vals.Int("nd"), vals.Duration("dd"))
		}
	})
	t.Run("a copy of the list", func(t *testing.T) {
		l := vals.StringList("l")
		l[0] = "changed"
		if again := vals.StringList("l"); again[0] != "a" {
			t.Error("StringList shares its backing array")
		}
	})
	for _, c := range []struct {
		name string
		read func()
		want string
	}{
		{"String of a Secret", func() { vals.String("k") }, `formschema: field "k" is secret, read as [string enum]`},
		{"Secret of a String", func() { vals.Secret("s") }, `formschema: field "s" is string, read as [secret]`},
		{"Int of an absent Bool", func() { vals.Int("on0") }, `formschema: field "on0" is bool, read as [int]`},
		{"Bool of an Int", func() { vals.Bool("n") }, `formschema: field "n" is int, read as [bool]`},
		{"Duration of an Int", func() { vals.Duration("nd") }, `formschema: field "nd" is int, read as [duration_ms]`},
		{"StringList of a String", func() { vals.StringList("s") }, `formschema: field "s" is string, read as [string_list]`},
		{"a key the schema lacks", func() { vals.String("nope") }, `formschema: no field "nope"`},
		{"a field of no Kind", func() { zero.String("z") }, `formschema: field "z" is kind 0, read as [string enum]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != c.want {
					t.Errorf("panic %v, want %q", got, c.want)
				}
			}()
			c.read()
		})
	}
}
