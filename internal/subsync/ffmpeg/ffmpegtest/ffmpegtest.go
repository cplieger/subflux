// Package ffmpegtest lets a test binary stand in for ffmpeg and ffprobe. A
// TestMain calling Main acts as the child env names when it is set; Path is
// the binary a test's Runner runs.
package ffmpegtest

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// env names the behaviour of the children a test starts, as Use sets it.
const env = "SUBFLUX_FAKE_FFMPEG"

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
}

// Main runs the tests, or acts as the child when env is set.
func Main(m *testing.M) {
	if b, ok := os.LookupEnv(env); ok {
		os.Exit(act(b))
	}
	os.Exit(m.Run())
}

// Path is the binary to run as ffmpeg and ffprobe: this test binary.
func Path() string { return os.Args[0] }

// Use makes the children t starts act as b, a behaviours entry. A child given
// a file: argument other than the runner's fd 3 exits 2.
func Use(t testing.TB, b string) { t.Setenv(env, b) }

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
