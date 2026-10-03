package required

import (
	"io"
	"testing"
)

type impl struct{}

func (*impl) Write(p []byte) (int, error) { return len(p), nil }

func TestMissing(t *testing.T) {
	t.Parallel()
	var typedNil *impl
	var boxedNil io.Writer = typedNil
	var nilMap map[string]int
	var nilFunc func()
	tests := []struct {
		name string
		v    any
		want bool
	}{
		{name: "untyped_nil", v: nil, want: true},
		{name: "nil_pointer_in_an_interface", v: boxedNil, want: true},
		{name: "nil_map", v: nilMap, want: true},
		{name: "nil_func", v: nilFunc, want: true},
		{name: "live_pointer", v: &impl{}, want: false},
		{name: "value", v: 0, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Missing(tt.v); got != tt.want {
				t.Errorf("Missing(%#v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
