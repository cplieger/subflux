package sentineltest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// recorder is a testing.TB whose failures are recorded, not reported.
type recorder struct {
	testing.TB
	failures []string
	fatal    bool
}

func (*recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatal(args ...any) {
	r.failures = append(r.failures, fmt.Sprint(args...))
	r.fatal = true
	runtime.Goexit()
}

// placedIn runs Placed in a package directory holding srcs as pkg.go and
// pkg2.go, beside a test file declaring ErrInTest.
func placedIn(t *testing.T, srcs [2]string, codes []Code, sentinels map[string]error, outside map[string]string) *recorder {
	t.Helper()
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg.go", srcs[0])
	write("pkg2.go", srcs[1])
	write("pkg_test.go", "package pkg\n\nvar ErrInTest = 1\n")
	if err := os.Mkdir(filepath.Join(dir, "sub.go"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Placed(r, codes, sentinels, outside)
	}()
	<-done
	return r
}

var errA, errB, errC = errors.New("a"), errors.New("b"), errors.New("c")

var threeSentinels = [2]string{`package pkg

import "errors"

var (
	ErrA = errors.New("a")
	ErrB = errors.New("b")
	errHidden = errors.New("hidden")
)

const ErrConst = "not a var"
`, `package pkg

import "errors"

var ErrC, Other = errors.New("c"), 1
`}

const noSentinel = "package pkg\n\nvar errHidden = 1\n"

func TestPlaced(t *testing.T) {
	all := map[string]error{"ErrA": errA, "ErrB": errB, "ErrC": errC}
	codes := []Code{{errA, "a"}, {errB, "b"}}
	cases := []struct {
		sentinels map[string]error
		outside   map[string]string
		name      string
		srcs      [2]string
		codes     []Code
		failures  int
		fatal     bool
	}{
		{name: "every sentinel placed", srcs: threeSentinels, codes: codes, sentinels: all, outside: map[string]string{"ErrC": "why"}},
		{name: "a sentinel neither coded nor outside", srcs: threeSentinels, codes: codes, sentinels: all, failures: 1},
		{name: "a sentinel both coded and outside", srcs: threeSentinels, codes: codes, sentinels: all, outside: map[string]string{"ErrA": "why", "ErrC": "why"}, failures: 1},
		{name: "a declared sentinel the table lacks", srcs: threeSentinels, codes: codes, sentinels: map[string]error{"ErrA": errA, "ErrB": errB}, failures: 1},
		{name: "a table entry the package lacks", srcs: threeSentinels, codes: codes, sentinels: map[string]error{"ErrA": errA, "ErrB": errB, "ErrC": errC, "ErrD": errC}, outside: map[string]string{"ErrC": "why", "ErrD": "why"}, failures: 1},
		{name: "outside names no sentinel", srcs: threeSentinels, codes: codes, sentinels: all, outside: map[string]string{"ErrC": "why", "ErrZ": "why"}, failures: 1},
		{name: "no sentinel declared", srcs: [2]string{noSentinel, noSentinel}, fatal: true, failures: 1},
		{name: "a file that does not parse", srcs: [2]string{threeSentinels[0], "package pkg\n\nvar (\n"}, fatal: true, failures: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := placedIn(t, tc.srcs, tc.codes, tc.sentinels, tc.outside)
			if len(r.failures) != tc.failures || r.fatal != tc.fatal {
				t.Errorf("failures %q, fatal %v; want %d failures, fatal %v", r.failures, r.fatal, tc.failures, tc.fatal)
			}
		})
	}
}

func TestCodeOf(t *testing.T) {
	codes := []Code{{errA, "a"}, {errB, "b"}, {errA, "a again"}}
	for _, tc := range []struct {
		err  error
		name string
		want string
	}{
		{fmt.Errorf("wrapped: %w", errB), "a wrapped sentinel", "b"},
		{errA, "the first match wins", "a"},
		{errC, "no match", ""},
		{nil, "nil", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeOf(codes, tc.err); got != tc.want {
				t.Errorf("CodeOf(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
