package previewhandlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	errMediaNotFound = "media file not found on server"
	styleFanart      = "fanart"
)

func buildVideoArgs(path string, startSec float64) []string {
	args := []string{"-hide_banner", "-loglevel", "error"}
	if startSec > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", startSec))
	}
	args = append(args, "-i", "file:"+path,
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-vf", "scale=-2:360", "-pix_fmt", "yuv420p",
		"-profile:v", "baseline", "-level", "3.0",
		"-c:a", "aac", "-ac", "2", "-b:a", "96k",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	)
	return args
}

func buildBufferedArgs(path string, startSec float64, outPath string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if startSec > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", startSec))
	}
	args = append(args, "-i", "file:"+path,
		"-t", strconv.Itoa(bufferedMaxDuration),
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		"-vf", "scale=-2:360", "-pix_fmt", "yuv420p",
		"-profile:v", "baseline", "-level", "3.0",
		"-c:a", "aac", "-ac", "2", "-b:a", "96k",
		"-movflags", "+faststart",
		"-f", "mp4", outPath,
	)
	return args
}

func responseStarted(w http.ResponseWriter) bool {
	return w.Header().Get("Content-Type") != ""
}

type ffmpegErrorRule struct {
	substr  string
	message string
}

var ffmpegErrorTable = []ffmpegErrorRule{
	{"No such file", errMediaNotFound},
	{"Permission denied", "media file not accessible"},
	{"Invalid data found", "media file is corrupted or unsupported format"},
	{"Connection refused", "cannot reach media server"},
	{"Connection timed out", "cannot reach media server"},
	{"Server returned 4", "media server returned an error"},
	{"Server returned 5", "media server returned an error"},
	{"Output file is empty", "ffmpeg produced no usable output"},
	{"moov atom not found", "media file has missing metadata"},
}

func classifyFFmpegError(stderr string) error {
	errMsg := strings.TrimSpace(stderr)
	if errMsg == "" {
		return errors.New("ffmpeg produced no output")
	}
	for _, rule := range ffmpegErrorTable {
		if strings.Contains(errMsg, rule.substr) {
			return errors.New(rule.message)
		}
	}
	return fmt.Errorf("ffmpeg: %s", errMsg)
}

func posterCoverURL(arrURL string, id int, style string) string {
	coverType := "poster-250.jpg"
	if style == styleFanart {
		coverType = "fanart-360.jpg"
	}
	return fmt.Sprintf("%s/api/v3/mediacover/%d/%s",
		strings.TrimRight(arrURL, "/"), id, coverType)
}

var limitedWriterPool = sync.Pool{
	New: func() any {
		return &limitedWriter{max: 8192}
	},
}

func getLimitedWriter() *limitedWriter {
	lw, ok := limitedWriterPool.Get().(*limitedWriter)
	if !ok || lw == nil {
		lw = &limitedWriter{max: 8192}
	}
	lw.b.Reset()
	return lw
}

func putLimitedWriter(lw *limitedWriter) {
	limitedWriterPool.Put(lw)
}

type limitedWriter struct {
	b   strings.Builder
	max int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := lw.max - lw.b.Len()
	if remaining <= 0 {
		return n, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	lw.b.Write(p)
	return n, nil
}

func (lw *limitedWriter) String() string { return lw.b.String() }
