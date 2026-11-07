package vad

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

// oracleFramesBinary is FramesBinary as it stood before the one-stream
// detector, under Tuning{Mode: 3, AdaptScale: 0}: a GMM pass of its own per
// threshold and overhang.
func oracleFramesBinary(pcm []int16, threshold float64, overhang int) []bool {
	v := newVADInst()
	const adaptScale = 0.0
	v.noiseUpdate = max(int16(float64(noiseUpdateConst)*adaptScale), 1)
	v.speechUpdate = max(int16(float64(speechUpdateConst)*adaptScale), 1)
	out := make([]bool, len(pcm)/FrameSamples)
	remaining := 0
	for i := range out {
		_, llr := v.processFrameLLR(pcm[i*FrameSamples : (i+1)*FrameSamples])
		switch {
		case llr >= threshold:
			out[i], remaining = true, overhang
		case remaining > 0:
			out[i] = true
			remaining--
		}
	}
	return out
}

// speechLike is a minute of 8 kHz audio: noise bursts of uneven length and
// level, a tone, and silences, so the model adapts and both deciders change
// state many times.
func speechLike(seed uint64) []int16 {
	var pcm []int16
	frames := noiseFrames(seed, 6000)
	for i, f := range frames {
		switch phase := (i / 50 * 7) % 9; {
		case phase < 4:
			scale := 0.05 + float64(phase)*0.3
			for _, s := range f {
				pcm = append(pcm, int16(float64(s)*scale))
			}
		case phase == 5:
			for j := range FrameSamples {
				pcm = append(pcm, int16(8000*math.Sin(float64(i*FrameSamples+j)*2*math.Pi*300/8000)))
			}
		default:
			pcm = append(pcm, make([]int16, FrameSamples)...)
		}
	}
	return pcm
}

// oneStream runs one detector over pcm and both production deciders over
// its ratios.
func oneStream(pcm []int16) (safe, precise []bool) {
	d := NewDetector()
	s, p := NewDecider(250, 35), NewDecider(125, 10)
	for i := 0; i+FrameSamples <= len(pcm); i += FrameSamples {
		llr := d.LLR(pcm[i : i+FrameSamples])
		safe, precise = append(safe, s.Speech(llr)), append(precise, p.Speech(llr))
	}
	return safe, precise
}

func TestOneLLRStreamMatchesTwoPasses(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3} {
		pcm := speechLike(seed)
		safe, precise := oneStream(pcm)
		for _, c := range []struct {
			name      string
			threshold float64
			overhang  int
			got       []bool
		}{{"safe", 250, 35, safe}, {"precise", 125, 10, precise}} {
			want := oracleFramesBinary(pcm, c.threshold, c.overhang)
			if len(c.got) != len(want) {
				t.Fatalf("seed %d %s: %d frames, want %d", seed, c.name, len(c.got), len(want))
			}
			speech := 0
			for i := range want {
				if c.got[i] != want[i] {
					t.Fatalf("seed %d %s: frame %d speech = %v, the two-pass oracle %v", seed, c.name, i, c.got[i], want[i])
				}
				if want[i] {
					speech++
				}
			}
			if speech == 0 || speech == len(want) {
				t.Fatalf("seed %d %s: %d of %d frames speech, want a mix", seed, c.name, speech, len(want))
			}
		}
	}
}

func TestOneLLRStreamMatchesTwoPassesOnAnyPCM(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pcm := rapid.SliceOfN(rapid.Int16(), 0, 200*FrameSamples).Draw(t, "pcm")
		safe, precise := oneStream(pcm)
		for name, got := range map[string][]bool{"safe": safe, "precise": precise} {
			threshold, overhang := 250.0, 35
			if name == "precise" {
				threshold, overhang = 125, 10
			}
			want := oracleFramesBinary(pcm, threshold, overhang)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s: frame %d speech = %v, oracle %v", name, i, got[i], want[i])
				}
			}
		}
	})
}
