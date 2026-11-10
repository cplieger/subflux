package syncsched_test

import (
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/subsync/vad"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// heapObjects is the runtime's live heap plus garbage not yet swept.
const heapObjects = "/memory/classes/heap/objects:bytes"

// held is the most heap f held above the heap before it, sampled every
// millisecond. The collector runs at GOGC 10 meanwhile, so garbage (the
// race detector's dropped sync.Pool puts among it) is freed within a tenth
// of the live heap and the peak measures what f keeps reachable.
func held(f func()) uint64 {
	sample := []metrics.Sample{{Name: heapObjects}}
	read := func() uint64 {
		metrics.Read(sample)
		return sample[0].Value.Uint64()
	}
	runtime.GC()
	base := read()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	var peak uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			peak = max(peak, read())
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	})
	f()
	close(stop)
	wg.Wait()
	if peak < base {
		return 0
	}
	return peak - base
}

func TestAudioPathMemoryBounded(t *testing.T) {
	r := newRig(t)
	ffmpegtest.UseFor(t, ffmpegtest.Probe, probeAnswer())
	ffmpegtest.UseFor(t, ffmpegtest.PCM, "pcm:"+strconv.Itoa(subsync.MaxAudioFrames*vad.FrameSamples))
	var got syncsched.Aligned
	var err error
	n := held(func() { got, err = r.align(t, srt(episode(6, 100)), thenAudio) })
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome.Method != timing.Audio || r.calls(ffmpegtest.PCM) != 1 {
		t.Fatalf("outcome %+v after %d PCM calls, want one audio pass", got.Outcome, r.calls(ffmpegtest.PCM))
	}
	if n >= 128<<20 {
		t.Errorf("an audio Align over %d frames held %d MiB at its peak, want under 128", subsync.MaxAudioFrames, n>>20)
	}
}
