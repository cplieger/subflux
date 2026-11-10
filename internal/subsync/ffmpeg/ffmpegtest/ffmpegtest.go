// Package ffmpegtest lets a test binary stand in for ffmpeg and ffprobe. A
// TestMain calling Main acts as the child env names when it is set; Path is
// the binary a test's Runner runs, and OnPath puts it on PATH.
package ffmpegtest

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// env names the behaviour of the children a test starts, as Use sets it;
// env_<call> one call's, as UseFor sets it; logEnv the file Calls reads.
const (
	env    = "SUBFLUX_FAKE_FFMPEG"
	logEnv = env + "_LOG"
)

// Call is one kind of runner call, told apart by its arguments.
type Call string

// The calls a Runner makes.
const (
	Probe   Call = "probe"
	Extract Call = "extract"
	PCM     Call = "pcm"
	Preview Call = "preview"
)

var calls = []Call{Probe, Extract, PCM, Preview}

func callEnv(c Call) string { return env + "_" + strings.ToUpper(string(c)) }

// callOf is the call the child's arguments make.
func callOf(args []string) Call {
	switch {
	case slices.Contains(args, "-show_streams"):
		return Probe
	case slices.Contains(args, "s16le"):
		return PCM
	case slices.Contains(args, "mp4"):
		return Preview
	}
	return Extract
}

// FloodBytes is how much "stderr-flood" writes to stderr.
const FloodBytes = 1 << 30

// FloodAt is the byte "stderr-flood" writes at offset i, so a test can
// rebuild any part of the stream.
func FloodAt(i int64) byte { return byte('a' + i%26) }

// videoArg is the one video argument the runner passes.
const videoArg = "file:/proc/self/fd/3"

// behaviours are what Use can name, "<name>" or "<name>:<arg>". Each gets the
// video, nil when the arguments name none, and returns the exit code.
var behaviours = map[string]func(arg string, video *os.File) int{
	// args prints the arguments, one per line.
	"args": func(string, *os.File) int { fmt.Print(strings.Join(os.Args[1:], "\n")); return 0 },
	// echo-video copies the video to stdout.
	"echo-video": echoVideo,
	// probe:<json> prints the JSON.
	"probe": func(arg string, _ *os.File) int { fmt.Print(arg); return 0 },
	// fail writes "boom" to stderr and exits 1.
	"fail": func(string, *os.File) int { fmt.Fprintln(os.Stderr, "boom"); return 1 },
	// stderr-flood writes FloodBytes to stderr, 1 KiB to stdout and exits 1.
	"stderr-flood": stderrFlood,
	// stdout-flood writes to stdout until it breaks, then hangs.
	"stdout-flood": stdoutFlood,
	// pcm:<n> writes samples 0 to n-1 as s16le.
	"pcm": func(arg string, _ *os.File) int {
		n, err := strconv.Atoi(arg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		return pcm(n)
	},
	// pcm-forever writes samples 0, 1, ... until stdout breaks, then hangs.
	"pcm-forever": func(string, *os.File) int { return pcm(-1) },
	// hang never exits.
	"hang": func(string, *os.File) int { hang(); return 0 },
	// stream-file:<dir> copies <dir>/<n> to stdout, n the stream "-map 0:<n>"
	// names; a stream with no file fails as "fail" does.
	"stream-file": func(dir string, _ *os.File) int {
		i := slices.Index(os.Args, "-map")
		if i < 0 || i+1 == len(os.Args) || !strings.HasPrefix(os.Args[i+1], "0:") {
			fmt.Fprintln(os.Stderr, "no -map 0:<n> argument")
			return 2
		}
		return cat(filepath.Join(dir, strings.TrimPrefix(os.Args[i+1], "0:")))
	},
	// cat:<path> copies the file at path to stdout.
	"cat": func(path string, _ *os.File) int { return cat(path) },
	// from-video:<name> reads a directory from the video, appends the call
	// to its "calls" file and copies <dir>/<name> to stdout, failing as
	// "fail" does when there is none: per-video answers, set once.
	"from-video": fromVideo,
}

// FromVideoCalls counts the calls of kind call that "from-video" logged in
// dir.
func FromVideoCalls(dir string, call Call) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, "calls"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return strings.Count(string(b), string(call)+"\n"), err
}

func fromVideo(name string, video *os.File) int {
	if video == nil {
		fmt.Fprintln(os.Stderr, "no video argument")
		return 2
	}
	dir, err := io.ReadAll(video)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := logCall(filepath.Join(string(dir), "calls"), callOf(os.Args[1:])); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return cat(filepath.Join(string(dir), name))
}

// Main runs the tests, or acts as the child when Use or UseFor set a
// behaviour: its call's, else the one for every call.
func Main(m *testing.M) {
	general, isChild := os.LookupEnv(env)
	for _, c := range calls {
		_, set := os.LookupEnv(callEnv(c))
		isChild = isChild || set
	}
	if !isChild {
		os.Exit(m.Run())
	}
	call := callOf(os.Args[1:])
	if path := os.Getenv(logEnv); path != "" {
		if err := logCall(path, call); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if b, ok := os.LookupEnv(callEnv(call)); ok {
		os.Exit(act(b))
	}
	os.Exit(act(general))
}

// Path is the binary to run as ffmpeg and ffprobe: this test binary.
func Path() string { return os.Args[0] }

// Use makes the children t starts act as b, a behaviours entry. A child given
// a file: argument other than the runner's fd 3 exits 2.
func Use(t testing.TB, b string) { t.Setenv(env, b) }

// UseFor makes the children t starts for call act as b, whatever Use set.
func UseFor(t testing.TB, call Call, b string) { t.Setenv(callEnv(call), b) }

// Calls records every child t starts from now on and returns a count of
// those making call.
func Calls(t testing.TB) func(call Call) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "calls")
	t.Setenv(logEnv, path)
	return func(call Call) int {
		b, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		return strings.Count(string(b), string(call)+"\n")
	}
}

// logCall appends call to the log at path; O_APPEND keeps concurrent
// children's lines whole.
func logCall(path string, call Call) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // G703: the path is Calls' own t.TempDir file
	if err != nil {
		return err
	}
	_, err = f.WriteString(string(call) + "\n")
	return errors.Join(err, f.Close())
}

// OnPath sets PATH to a directory holding this test binary under each of
// names ("ffmpeg", "ffprobe") and nothing else, for code running a zero
// Runner; with no names, no binary resolves.
func OnPath(t testing.TB, names ...string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range names {
		if err := os.Symlink(self, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func act(behaviour string) int {
	name, arg, _ := strings.Cut(behaviour, ":")
	run, ok := behaviours[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown behaviour %q\n", behaviour)
		return 2
	}
	var video *os.File
	if i := slices.IndexFunc(os.Args[1:], func(a string) bool { return strings.HasPrefix(a, "file:") }); i >= 0 {
		if os.Args[1+i] != videoArg {
			fmt.Fprintf(os.Stderr, "video argument %q, want %q\n", os.Args[1+i], videoArg)
			return 2
		}
		video = os.NewFile(3, "video")
	}
	return run(arg, video)
}

func cat(path string) int {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return 1
	}
	return 0
}

func echoVideo(_ string, video *os.File) int {
	if video == nil {
		fmt.Fprintln(os.Stderr, "no video argument")
		return 2
	}
	if _, err := io.Copy(os.Stdout, video); err != nil {
		return 1
	}
	return 0
}

func stderrFlood(string, *os.File) int {
	// FloodAt repeats every 26 bytes, so each block is a window of one
	// precomputed pattern: the child spends its time writing, and the
	// call's duration measures the runner's drain, not this loop.
	const blockLen = 1 << 20
	pattern := make([]byte, blockLen+26)
	for i := range pattern {
		pattern[i] = FloodAt(int64(i))
	}
	for off := int64(0); off < FloodBytes; off += blockLen {
		if _, err := os.Stderr.Write(pattern[off%26 : off%26+blockLen]); err != nil {
			return 1
		}
	}
	_, _ = os.Stdout.Write(make([]byte, 1<<10))
	return 1
}

func stdoutFlood(string, *os.File) int {
	signal.Ignore(syscall.SIGPIPE)
	block := make([]byte, 64<<10)
	for {
		if _, err := os.Stdout.Write(block); err != nil {
			hang()
		}
	}
}

// pcm writes samples 0 to n-1, or forever when n is negative.
func pcm(n int) int {
	signal.Ignore(syscall.SIGPIPE)
	out := bufio.NewWriterSize(os.Stdout, 32<<10)
	var sample [2]byte
	for i := 0; n < 0 || i < n; i++ {
		binary.LittleEndian.PutUint16(sample[:], uint16(int16(i)))
		if _, err := out.Write(sample[:]); err != nil {
			hang()
		}
	}
	if err := out.Flush(); err != nil {
		hang()
	}
	return 0
}

// hang stands in for a child that stops responding. A bare select would be
// the runtime's deadlock exit, not a hang.
func hang() {
	for {
		time.Sleep(time.Hour)
	}
}
