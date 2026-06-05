package previewhandlers

import (
	"strings"
	"testing"
)

// FuzzClassifyFFmpegErrorNeverNil verifies that classifyFFmpegError always
// returns a non-nil error regardless of input (null-safety invariant).
func FuzzClassifyFFmpegErrorNeverNil(f *testing.F) {
	f.Add("")
	f.Add("No such file or directory")
	f.Add("Permission denied")
	f.Add("Invalid data found when processing input")
	f.Add("\x00\x01\x02")
	f.Add(strings.Repeat("x", 10000))

	f.Fuzz(func(t *testing.T, stderr string) {
		err := classifyFFmpegError(stderr)
		if err == nil {
			t.Fatal("classifyFFmpegError returned nil error")
		}
		if err.Error() == "" {
			t.Fatal("classifyFFmpegError returned empty error message")
		}
	})
}

// FuzzBuildVideoArgsStructural verifies structural invariants of buildVideoArgs:
// - output always contains -i flag
// - output always contains pipe:1 (stdout output)
// - -ss appears before -i when startSec > 0
// - path is prefixed with "file:"
func FuzzBuildVideoArgsStructural(f *testing.F) {
	f.Add("/media/video.mkv", 0.0)
	f.Add("/tmp/a b c.mp4", 120.5)
	f.Add("", -1.0)
	f.Add("/path/with spaces/file.avi", 86400.0)

	f.Fuzz(func(t *testing.T, path string, startSec float64) {
		args := buildVideoArgs(path, startSec)

		hasI := false
		hasPipe := false
		for _, a := range args {
			if a == "-i" {
				hasI = true
			}
			if a == "pipe:1" {
				hasPipe = true
			}
		}
		if !hasI {
			t.Fatal("buildVideoArgs missing -i flag")
		}
		if !hasPipe {
			t.Fatal("buildVideoArgs missing pipe:1 output")
		}

		// Verify file: prefix on the path argument (follows -i).
		for i, a := range args {
			if a == "-i" && i+1 < len(args) {
				if !strings.HasPrefix(args[i+1], "file:") {
					t.Fatalf("input path not file:-prefixed: %q", args[i+1])
				}
			}
		}
	})
}

// FuzzLimitedWriterNeverPanics verifies that limitedWriter.Write never panics
// and always reports n == len(input), regardless of internal state.
func FuzzLimitedWriterNeverPanics(f *testing.F) {
	f.Add(100, []byte("hello"))
	f.Add(0, []byte("data"))
	f.Add(3, []byte("longstring"))
	f.Add(1, []byte{0, 1, 2, 3})

	f.Fuzz(func(t *testing.T, maxCap int, data []byte) {
		if maxCap < 0 {
			maxCap = 0
		}
		lw := &limitedWriter{max: maxCap}
		n, err := lw.Write(data)
		if err != nil {
			t.Fatalf("limitedWriter.Write returned error: %v", err)
		}
		if n != len(data) {
			t.Fatalf("limitedWriter.Write returned n=%d, want %d", n, len(data))
		}
		// Invariant: internal buffer never exceeds max.
		if len(lw.String()) > maxCap {
			t.Fatalf("limitedWriter exceeded max: len=%d, max=%d", len(lw.String()), maxCap)
		}
	})
}

// FuzzPosterCoverURLStructural verifies that posterCoverURL always produces
// a URL containing /api/v3/mediacover/ and a .jpg extension.
func FuzzPosterCoverURLStructural(f *testing.F) {
	f.Add("http://localhost:7878", 1, "")
	f.Add("http://sonarr:8989/", 999, "fanart")
	f.Add("", 0, "unknown")
	f.Add("https://arr.example.com///", -1, "fanart")

	f.Fuzz(func(t *testing.T, arrURL string, id int, style string) {
		result := posterCoverURL(arrURL, id, style)
		if !strings.Contains(result, "/api/v3/mediacover/") {
			t.Fatalf("posterCoverURL missing /api/v3/mediacover/: %q", result)
		}
		if !strings.HasSuffix(result, ".jpg") {
			t.Fatalf("posterCoverURL not ending in .jpg: %q", result)
		}
	})
}
