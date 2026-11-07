package ffmpeg

import (
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// runtimeName is the name ffmpeg's -decoders, -encoders and -muxers print
// for a configure name, where the two differ.
var runtimeName = map[feature]string{
	{decoder, "movtext"}: "mov_text",
	{muxer, "pcm_s16le"}: "s16le",
}

func (f feature) runtime() string {
	if n, ok := runtimeName[f]; ok {
		return n
	}
	return f.Name
}

var (
	enableList = regexp.MustCompile(`--enable-(decoder|encoder|muxer|demuxer|filter|protocol)=([\w,]+)`)
	readback   = regexp.MustCompile(`-hide_banner -(decoder|encoder|muxer|filter)s \| grep -cE '[^(]*\(([\w|]+)\) '\)" = (\d+)`)
)

// buildLists reads the Dockerfile's ffmpeg configure lists and the readback
// checks run on the built binary, each by kind.
func buildLists(t *testing.T) (enabled, checked map[kind][]string) {
	t.Helper()
	b, err := os.ReadFile("../../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	enabled, checked = map[kind][]string{}, map[kind][]string{}
	for _, m := range enableList.FindAllStringSubmatch(string(b), -1) {
		enabled[kind(m[1])] = append(enabled[kind(m[1])], strings.Split(m[2], ",")...)
	}
	for _, m := range readback.FindAllStringSubmatch(string(b), -1) {
		kind, names := kind(m[1]), strings.Split(m[2], "|")
		if _, dup := checked[kind]; dup {
			t.Fatalf("two %s readback checks", kind)
		}
		if n, _ := strconv.Atoi(m[3]); n != len(names) {
			t.Errorf("%s readback expects %d of its %d names", kind, n, len(names))
		}
		checked[kind] = names
	}
	if len(enabled) == 0 || len(checked) == 0 {
		t.Fatal("found no configure lists or readback checks in the Dockerfile")
	}
	return enabled, checked
}

func TestFFmpegBuildCoversArgs(t *testing.T) {
	enabled, checked := buildLists(t)
	want := map[kind][]string{}
	for _, f := range features {
		if !slices.Contains(enabled[f.Kind], f.Name) {
			t.Errorf("the build does not enable %s %s", f.Kind, f.Name)
		}
		want[f.Kind] = append(want[f.Kind], f.Name)
	}
	t.Run("only the runner's encoders, muxers, filters and protocols", func(t *testing.T) {
		for _, k := range []kind{encoder, muxer, filter, protocol} {
			if got := slices.Sorted(slices.Values(enabled[k])); !slices.Equal(got, slices.Sorted(slices.Values(want[k]))) {
				t.Errorf("%s list = %v, want the runner's %v", k, got, want[k])
			}
		}
	})
	t.Run("every readback names the runner's entries", func(t *testing.T) {
		for k, names := range checked {
			var runtime []string
			for _, f := range features {
				if f.Kind == k {
					runtime = append(runtime, f.runtime())
				}
			}
			if got, w := slices.Sorted(slices.Values(names)), slices.Sorted(slices.Values(runtime)); !slices.Equal(got, w) {
				t.Errorf("%s readback checks %v, want %v", k, got, w)
			}
		}
		if got := slices.Sorted(maps.Keys(checked)); !slices.Equal(got, []kind{decoder, encoder, filter, muxer}) {
			t.Errorf("readback checks for %v, want decoder, encoder, filter and muxer", got)
		}
	})
}
