package vad

import (
	"math"
	"math/rand/v2"
	"testing"

	"pgregory.net/rapid"
)

// loudFrame returns one 80-sample 440Hz sine frame at amplitude 20000.
func loudFrame() []int16 {
	f := make([]int16, 80)
	for i := range f {
		f[i] = int16(20000 * math.Sin(float64(i)*2*math.Pi*440/8000))
	}
	return f
}

// noiseFrames returns n frames of loud broadband noise from seed, which mode 3
// reads as speech where a pure tone does not.
func noiseFrames(seed uint64, n int) [][]int16 {
	r := rand.New(rand.NewPCG(seed, 0))
	out := make([][]int16, n)
	for i := range out {
		out[i] = make([]int16, FrameSamples)
		for j := range out[i] {
			out[i][j] = int16(r.IntN(30_000) - 15_000)
		}
	}
	return out
}

// --- newVADInst ---

func TestNewVADInstIsMode3(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	if v.localThresh != 94 {
		t.Errorf("newVADInst().localThresh = %d, want 94", v.localThresh)
	}
	if v.globalThresh != 1100 {
		t.Errorf("newVADInst().globalThresh = %d, want 1100", v.globalThresh)
	}
	if v.overHangMax1 != 6 {
		t.Errorf("newVADInst().overHangMax1 = %d, want 6", v.overHangMax1)
	}
	if v.overHangMax2 != 9 {
		t.Errorf("newVADInst().overHangMax2 = %d, want 9", v.overHangMax2)
	}
	if v.noiseUpdate != noiseUpdateConst {
		t.Errorf("newVADInst().noiseUpdate = %d, want %d", v.noiseUpdate, noiseUpdateConst)
	}
	if v.speechUpdate != speechUpdateConst {
		t.Errorf("newVADInst().speechUpdate = %d, want %d", v.speechUpdate, speechUpdateConst)
	}
}

func TestNewVADInst_initial_means_copied(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	for i := range vadTableSize {
		if v.noiseMeans[i] != initNoiseMeans[i] {
			t.Errorf("noiseMeans[%d] = %d, want %d", i, v.noiseMeans[i], initNoiseMeans[i])
		}
		if v.speechMeans[i] != initSpeechMeans[i] {
			t.Errorf("speechMeans[%d] = %d, want %d", i, v.speechMeans[i], initSpeechMeans[i])
		}
	}
}

func TestNewVADInst_lowValue_initialized(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	for i := range v.lowValue {
		if v.lowValue[i] != 10000 {
			t.Errorf("lowValue[%d] = %d, want 10000", i, v.lowValue[i])
		}
	}
}

func TestNewVADInst_meanVal_initialized(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	for i := range v.meanVal {
		if v.meanVal[i] != 1600 {
			t.Errorf("meanVal[%d] = %d, want 1600", i, v.meanVal[i])
		}
	}
}

// --- processFrameLLR ---

func TestProcessFrameLLR_silence(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	frame := make([]int16, 80)
	flag, llr := v.processFrameLLR(frame)
	// Silent frame should not be classified as speech.
	if flag != 0 {
		t.Errorf("processFrameLLR(silence) flag = %d, want 0", flag)
	}
	// Silent frame: totalPower <= minEnergy, so GMM is skipped and llr = 0.
	if llr != 0 {
		t.Errorf("processFrameLLR(silence) llr = %f, want 0", llr)
	}
}

func TestProcessFrameLLR_loud_signal(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	// Generate a loud 440Hz sine signal.
	frame := make([]int16, 80)
	for i := range frame {
		frame[i] = int16(10000 * math.Sin(float64(i)*2*math.Pi*440/8000))
	}
	flag, llr := v.processFrameLLR(frame)
	if flag != 1 {
		t.Errorf("processFrameLLR(loud) flag = %d, want 1 (speech)", flag)
	}
	// LLR should be positive (speech-like features).
	if llr <= 0 {
		t.Errorf("processFrameLLR(loud) llr = %f, want > 0", llr)
	}
}

// TestProcessFrameLLR_near_energy_threshold exercises the totalPower <=
// minEnergy boundary: a very low-amplitude frame (all samples = 1) has energy
// near vadMinEnergy, so the GMM is skipped or returns near-zero and the frame
// is not classified as speech.
func TestProcessFrameLLR_near_energy_threshold(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	frame := make([]int16, 80)
	for i := range frame {
		frame[i] = 1
	}
	flag, llr := v.processFrameLLR(frame)
	if flag != 0 {
		t.Errorf("processFrameLLR(near-threshold) flag = %d, want 0", flag)
	}
	if math.IsNaN(llr) || math.IsInf(llr, 0) {
		t.Errorf("processFrameLLR(near-threshold) llr = %f, want finite", llr)
	}
}

// TestProcessFrameLLR_overhang_hangover verifies the speech-to-silence
// hangover: after sustained speech the overhang keeps the decision positive
// for several silent frames before it expires.
func TestProcessFrameLLR_overhang_hangover(t *testing.T) {
	t.Parallel()
	v := newVADInst()
	for _, f := range noiseFrames(1, 20) {
		v.processFrameLLR(f)
	}
	if v.numOfSpeech == 0 {
		t.Fatal("numOfSpeech = 0 after 20 loud broadband frames, want > 0")
	}
	// Now feed silent frames. The overhang should keep flag > 0 for some frames.
	silent := make([]int16, 80)
	hangoverCount := 0
	for range 20 {
		flag, _ := v.processFrameLLR(silent)
		if flag > 0 {
			hangoverCount++
		}
	}
	if hangoverCount == 0 {
		t.Error("no hangover frames detected after speech-to-silence transition")
	}
}

func TestProcessFrameLLR_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		frame := make([]int16, 80)
		for i := range frame {
			frame[i] = rapid.Int16().Draw(t, "sample")
		}
		v := newVADInst()
		flag, llr := v.processFrameLLR(frame)
		if flag < 0 || flag > 1 {
			t.Fatalf("flag = %d, want 0 or 1", flag)
		}
		if math.IsNaN(llr) || math.IsInf(llr, 0) {
			t.Fatalf("llr = %f, want finite", llr)
		}
	})
}

// --- Detector and Decider ---

func TestNewDetectorAdaptsAtTheSmallestStep(t *testing.T) {
	t.Parallel()
	d := NewDetector()
	if d.v.noiseUpdate != 1 || d.v.speechUpdate != 1 {
		t.Errorf("updates = %d, %d, want 1, 1", d.v.noiseUpdate, d.v.speechUpdate)
	}
	if d.v.localThresh != mode3Local || d.v.globalThresh != mode3Global {
		t.Errorf("thresholds = %d, %d, want mode 3's", d.v.localThresh, d.v.globalThresh)
	}
}

func TestDetectorReadsOnlyOneFrame(t *testing.T) {
	t.Parallel()
	long := append(loudFrame(), loudFrame()...)
	a, b := NewDetector(), NewDetector()
	if got, want := a.LLR(long), b.LLR(loudFrame()); got != want {
		t.Errorf("LLR over a two-frame slice = %v, want the first frame's %v", got, want)
	}
}

func TestDeciderThresholdAndOverhang(t *testing.T) {
	t.Parallel()
	d := NewDecider(100, 2)
	llrs := []float64{99, 100, 0, 0, 0, 250, 0, 99.9}
	want := []bool{false, true, true, true, false, true, true, true}
	for i, llr := range llrs {
		if got := d.Speech(llr); got != want[i] {
			t.Errorf("frame %d (llr %v): Speech = %v, want %v", i, llr, got, want[i])
		}
	}
}

func TestDeciderWithNoOverhang(t *testing.T) {
	t.Parallel()
	d := NewDecider(125, 0)
	for i, llr := range []float64{125, 124} {
		if got, want := d.Speech(llr), i == 0; got != want {
			t.Errorf("frame %d: Speech = %v, want %v", i, got, want)
		}
	}
}
