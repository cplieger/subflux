package settings

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/cplieger/go/slogx/capture"
)

func TestProcessLoggerFollowsTheDefault(t *testing.T) {
	derived := processLogger.With("a", 1).WithGroup("g")
	var buf bytes.Buffer
	capture.SwapDefault(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	derived.Info("dropped")
	derived.Warn("probe", "b", 2)
	base := processLogger.With("p", 1).With("q", 2).With("r", 3)
	x, y := base.With("x", 1), base.With("y", 2)
	x.Warn("sibling")
	y.Warn("other")
	got := buf.String()
	if strings.Contains(got, "dropped") {
		t.Errorf("an INFO line passed the default's WARN level:\n%s", got)
	}
	if !strings.Contains(got, "msg=probe a=1 g.b=2") {
		t.Errorf("the derived logger lost its attrs or group in the default installed after it:\n%s", got)
	}
	if !strings.Contains(got, "msg=sibling p=1 q=2 r=3 x=1\n") {
		t.Errorf("a sibling derivation overwrote another's attrs:\n%s", got)
	}
}
