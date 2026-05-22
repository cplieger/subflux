package previewhandlers

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

// --- buildVideoArgs ---

func TestBuildVideoArgs(t *testing.T) {
	t.Parallel()

	t.Run("no start offset", func(t *testing.T) {
		t.Parallel()
		args := buildVideoArgs("/media/movie.mkv", 0)

		for _, a := range args {
			if a == "-ss" {
				t.Error("buildVideoArgs(0) should not contain -ss")
			}
		}
		assertContains(t, args, "file:/media/movie.mkv",
			"buildVideoArgs() missing file: prefixed input path")
		assertContains(t, args, "pipe:1",
			"buildVideoArgs() missing pipe:1 output")
		assertContains(t, args, "frag_keyframe+empty_moov+default_base_moof",
			"buildVideoArgs() missing fragmented movflags")
	})

	t.Run("with start offset", func(t *testing.T) {
		t.Parallel()
		args := buildVideoArgs("/media/movie.mkv", 120.5)

		assertContains(t, args, "-ss",
			"buildVideoArgs(120.5) should contain -ss")
		assertContains(t, args, "120.500",
			"buildVideoArgs(120.5) should contain formatted offset")
	})

	t.Run("encoding settings", func(t *testing.T) {
		t.Parallel()
		args := buildVideoArgs("/media/movie.mkv", 0)

		assertContains(t, args, "libx264",
			"buildVideoArgs() should use libx264 codec")
		assertContains(t, args, "ultrafast",
			"buildVideoArgs() should use ultrafast preset")
		assertContains(t, args, "scale=-2:360",
			"buildVideoArgs() should scale to 360p")
		assertContains(t, args, "aac",
			"buildVideoArgs() should use aac audio codec")
	})

	t.Run("ss appears before input for fast seek", func(t *testing.T) {
		t.Parallel()
		args := buildVideoArgs("/media/movie.mkv", 30.0)

		ssIdx := slices.Index(args, "-ss")
		iIdx := slices.Index(args, "-i")
		if ssIdx < 0 || iIdx < 0 {
			t.Fatal("buildVideoArgs(30) missing -ss or -i")
		}
		if ssIdx >= iIdx {
			t.Errorf("buildVideoArgs(30): -ss at index %d must appear before -i at index %d for fast seek",
				ssIdx, iIdx)
		}
	})
}

// --- buildBufferedArgs ---

func TestBuildBufferedArgs(t *testing.T) {
	t.Parallel()

	t.Run("no start offset", func(t *testing.T) {
		t.Parallel()
		args := buildBufferedArgs("/media/movie.mkv", 0, "/tmp/out.mp4")

		for _, a := range args {
			if a == "-ss" {
				t.Error("buildBufferedArgs(0) should not contain -ss")
			}
		}
		assertContains(t, args, "/tmp/out.mp4",
			"buildBufferedArgs() missing output path")
		assertContains(t, args, "+faststart",
			"buildBufferedArgs() missing faststart movflag")
		assertContains(t, args, "-y",
			"buildBufferedArgs() missing -y flag")
		assertContains(t, args, "-t",
			"buildBufferedArgs() missing -t duration limit")
	})

	t.Run("with start offset", func(t *testing.T) {
		t.Parallel()
		args := buildBufferedArgs("/media/movie.mkv", 60.0, "/tmp/out.mp4")

		assertContains(t, args, "-ss",
			"buildBufferedArgs(60) should contain -ss")
		assertContains(t, args, "60.000",
			"buildBufferedArgs(60) should contain formatted offset")
	})

	t.Run("uses file prefix for input", func(t *testing.T) {
		t.Parallel()
		args := buildBufferedArgs("/media/movie.mkv", 0, "/tmp/out.mp4")

		assertContains(t, args, "file:/media/movie.mkv",
			"buildBufferedArgs() missing file: prefixed input path")
	})

	t.Run("duration limit matches constant", func(t *testing.T) {
		t.Parallel()
		args := buildBufferedArgs("/media/movie.mkv", 0, "/tmp/out.mp4")

		assertContains(t, args, strconv.Itoa(bufferedMaxDuration),
			"buildBufferedArgs() duration should match bufferedMaxDuration")
	})

	t.Run("ss appears before input for fast seek", func(t *testing.T) {
		t.Parallel()
		args := buildBufferedArgs("/media/movie.mkv", 30.0, "/tmp/out.mp4")

		ssIdx := slices.Index(args, "-ss")
		iIdx := slices.Index(args, "-i")
		if ssIdx < 0 || iIdx < 0 {
			t.Fatal("buildBufferedArgs(30) missing -ss or -i")
		}
		if ssIdx >= iIdx {
			t.Errorf("buildBufferedArgs(30): -ss at index %d must appear before -i at index %d for fast seek",
				ssIdx, iIdx)
		}
	})
}

// assertContains checks that a string slice contains the expected value.
func assertContains(t *testing.T, args []string, want, msg string) {
	t.Helper()
	if !slices.Contains(args, want) {
		t.Errorf("%s: %v does not contain %q", msg, args, want)
	}
}

// --- responseStarted ---

func TestResponseStarted(t *testing.T) {
	t.Parallel()

	t.Run("returns false when no content-type set", func(t *testing.T) {
		t.Parallel()
		w := &fakeResponseWriter{headers: make(map[string][]string)}
		if responseStarted(w) {
			t.Error("responseStarted() = true, want false (no Content-Type)")
		}
	})

	t.Run("returns true when content-type set", func(t *testing.T) {
		t.Parallel()
		w := &fakeResponseWriter{headers: map[string][]string{
			"Content-Type": {"video/mp4"},
		}}
		if !responseStarted(w) {
			t.Error("responseStarted() = false, want true (Content-Type set)")
		}
	})

	t.Run("returns false when content-type is empty string", func(t *testing.T) {
		t.Parallel()
		w := &fakeResponseWriter{headers: map[string][]string{
			"Content-Type": {""},
		}}
		if responseStarted(w) {
			t.Error("responseStarted() = true, want false (empty Content-Type)")
		}
	})
}

// fakeResponseWriter is a minimal http.ResponseWriter for responseStarted tests.
type fakeResponseWriter struct {
	headers map[string][]string
}

func (f *fakeResponseWriter) Header() http.Header { return http.Header(f.headers) }

func (f *fakeResponseWriter) Write(_ []byte) (int, error) { return 0, nil }

func (f *fakeResponseWriter) WriteHeader(_ int) {}

// --- classifyFFmpegError ---

func TestClassifyFFmpegError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name:   "empty stderr returns default message",
			stderr: "",
			want:   "ffmpeg produced no output",
		},
		{
			name:   "whitespace-only stderr returns default message",
			stderr: "   \n\t  ",
			want:   "ffmpeg produced no output",
		},
		{
			name:   "no such file maps to not found",
			stderr: "/media/movie.mkv: No such file or directory\n",
			want:   "media file not found on server",
		},
		{
			name:   "permission denied maps to not accessible",
			stderr: "/media/movie.mkv: Permission denied\n",
			want:   "media file not accessible",
		},
		{
			name:   "generic error passes through with prefix",
			stderr: "Avi duration estimate overflow\n",
			want:   "ffmpeg: Avi duration estimate overflow",
		},
		{
			name:   "no such file takes priority over permission denied",
			stderr: "No such file or directory Permission denied",
			want:   "media file not found on server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyFFmpegError(tt.stderr)
			if got.Error() != tt.want {
				t.Errorf("classifyFFmpegError(%q) = %q, want %q",
					tt.stderr, got.Error(), tt.want)
			}
		})
	}
}

// --- posterCoverURL ---

func TestPosterCoverURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		arrURL string
		style  string
		want   string
		id     int
	}{
		{
			name:   "default poster style",
			arrURL: "http://radarr:7878",
			id:     123,
			style:  "",
			want:   "http://radarr:7878/api/v3/mediacover/123/poster-250.jpg",
		},
		{
			name:   "fanart style",
			arrURL: "http://sonarr:8989",
			id:     456,
			style:  "fanart",
			want:   "http://sonarr:8989/api/v3/mediacover/456/fanart-360.jpg",
		},
		{
			name:   "unknown style falls back to poster",
			arrURL: "http://radarr:7878",
			id:     1,
			style:  "banner",
			want:   "http://radarr:7878/api/v3/mediacover/1/poster-250.jpg",
		},
		{
			name:   "trailing slash on arrURL is trimmed",
			arrURL: "http://radarr:7878/",
			id:     99,
			style:  "fanart",
			want:   "http://radarr:7878/api/v3/mediacover/99/fanart-360.jpg",
		},
		{
			name:   "multiple trailing slashes trimmed",
			arrURL: "http://radarr:7878///",
			id:     42,
			style:  "",
			want:   "http://radarr:7878/api/v3/mediacover/42/poster-250.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := posterCoverURL(tt.arrURL, tt.id, tt.style)
			if got != tt.want {
				t.Errorf("posterCoverURL(%q, %d, %q) = %q, want %q",
					tt.arrURL, tt.id, tt.style, got, tt.want)
			}
		})
	}
}

// --- limitedWriter ---

func TestLimitedWriter_Write_under_cap(t *testing.T) {
	t.Parallel()
	lw := &limitedWriter{max: 100}
	n, err := lw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("limitedWriter.Write() error = %v, want nil", err)
	}
	if n != 5 {
		t.Errorf("limitedWriter.Write() = %d, want 5", n)
	}
	if got := lw.String(); got != "hello" {
		t.Errorf("limitedWriter.String() = %q, want %q", got, "hello")
	}
}

func TestLimitedWriter_Write_at_cap(t *testing.T) {
	t.Parallel()
	lw := &limitedWriter{max: 5}
	lw.Write([]byte("hello"))

	n, err := lw.Write([]byte("world"))
	if err != nil {
		t.Fatalf("limitedWriter.Write() error = %v, want nil", err)
	}
	if n != 5 {
		t.Errorf("limitedWriter.Write() = %d, want 5 (reports full len even when capped)", n)
	}
	if got := lw.String(); got != "hello" {
		t.Errorf("limitedWriter.String() = %q, want %q", got, "hello")
	}
}

func TestLimitedWriter_Write_partial_truncation(t *testing.T) {
	t.Parallel()
	lw := &limitedWriter{max: 8}
	lw.Write([]byte("hello"))

	n, err := lw.Write([]byte("world"))
	if err != nil {
		t.Fatalf("limitedWriter.Write() error = %v, want nil", err)
	}
	if n != 5 {
		t.Errorf("limitedWriter.Write() = %d, want 5 (reports full len even when truncated)", n)
	}
	if got := lw.String(); got != "hellowor" {
		t.Errorf("limitedWriter.String() = %q, want %q", got, "hellowor")
	}
}

func TestLimitedWriter_Write_zero_cap(t *testing.T) {
	t.Parallel()
	lw := &limitedWriter{max: 0}
	n, err := lw.Write([]byte("data"))
	if err != nil {
		t.Fatalf("limitedWriter.Write() error = %v, want nil", err)
	}
	if n != 4 {
		t.Errorf("limitedWriter.Write() = %d, want 4", n)
	}
	if got := lw.String(); got != "" {
		t.Errorf("limitedWriter.String() = %q, want %q", got, "")
	}
}

func TestLimitedWriter_String_empty(t *testing.T) {
	t.Parallel()
	lw := &limitedWriter{max: 100}
	if got := lw.String(); got != "" {
		t.Errorf("limitedWriter.String() = %q, want %q", got, "")
	}
}
