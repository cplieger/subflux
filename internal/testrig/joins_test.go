package testrig

import (
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
)

// subfluxCode prefixes every function a subflux package defines.
const subfluxCode = "github.com/cplieger/subflux/internal/"

// subfluxGoroutines returns the stack of every goroutine but the caller's
// running subflux code or started by it. A frame is matched rather than the
// creator alone because sync.WaitGroup.Go names itself the creator.
func subfluxGoroutines() []string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []string
	for i, g := range strings.Split(string(buf), "\n\n") {
		if i == 0 {
			continue
		}
		for line := range strings.SplitSeq(g, "\n") {
			if strings.HasPrefix(strings.TrimPrefix(line, "created by "), subfluxCode) {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

func TestRunJoinsEveryGoroutine(t *testing.T) {
	srv := arrtest.Load(t, sonarrRecording)
	reached := make(chan struct{}, 1)
	srv.Handle("/api/v5/rootfolder", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	})
	unmapped := "sources:\n  - {id: tv, type: sonarr, url: '" + srv.Start(t) + "', api_key: " + arrtest.Key + "}\n"
	r := Start(t, Options{Config: []byte(mediaLanguages + unmapped)})
	select {
	case <-reached:
	case <-time.After(readyBudget):
		t.Fatal("the media writer's root-folder fill never reached Sonarr")
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("app.Run = %v", err)
	}
	var left []string
	if !within(2*time.Second, func() bool { left = subfluxGoroutines(); return len(left) == 0 }) {
		t.Errorf("app.Run returned with %d subflux goroutines running:\n\n%s", len(left), strings.Join(left, "\n\n"))
	}
}
