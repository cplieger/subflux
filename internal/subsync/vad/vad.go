// Package vad is a line-by-line Go port of WebRTC's voice activity detector
// (chromium/webrtc common_audio/vad/, BSD-3-Clause, Copyright (c) 2012 The
// WebRTC project authors): the allpass filter bank, log energy features, GMM
// classifier and adaptive model update. A Detector yields one log-likelihood
// ratio per 10 ms frame and Deciders threshold that one stream.
package vad

const (
	vadNumCh       = 6
	vadNumGauss    = 2
	vadTableSize   = vadNumCh * vadNumGauss
	vadMinEnergy   = 10
	vadMaxSpeechFr = 6
	vadMinStd      = 384
)

// vadInst holds the full VAD state (equivalent to VadInstT).
type vadInst struct {
	// GMM model parameters (2 Gaussians × 6 channels = 12 values each).
	noiseMeans  [vadTableSize]int16
	speechMeans [vadTableSize]int16
	noiseStds   [vadTableSize]int16
	speechStds  [vadTableSize]int16

	// Filter states.
	upperState [5]int16
	lowerState [5]int16
	hpState    [4]int16

	// Minimum value tracking for long-term noise correction.
	lowValue [16 * vadNumCh]int16
	meanVal  [vadNumCh]int16
	indexVec [16 * vadNumCh]int16

	// Overhang smoothing.
	overHang     int16
	numOfSpeech  int16
	overHangMax1 int16
	overHangMax2 int16
	localThresh  int16
	globalThresh int16

	// Adaptation speed (Q15).
	noiseUpdate  int16
	speechUpdate int16

	frameCounter int32
}

// Initial GMM parameters (from vad_core.c).
var (
	initNoiseMeans  = [vadTableSize]int16{6738, 4892, 7065, 6715, 6771, 3369, 7646, 3863, 7820, 7266, 5020, 4362}
	initSpeechMeans = [vadTableSize]int16{8306, 10085, 10078, 11823, 11843, 6309, 9473, 9571, 10879, 7581, 8180, 7483}
	initNoiseStds   = [vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455}
	initSpeechStds  = [vadTableSize]int16{555, 505, 567, 524, 585, 1231, 509, 828, 492, 1540, 1079, 850}
	noiseWt         = [vadTableSize]int16{34, 62, 72, 66, 53, 25, 94, 66, 56, 62, 75, 103}
	speechWt        = [vadTableSize]int16{48, 82, 45, 87, 50, 47, 80, 46, 83, 41, 78, 81}
	specWeight      = [vadNumCh]int16{6, 8, 10, 12, 14, 16}
	offsets         = [vadNumCh]int16{368, 368, 272, 176, 176, 176}
	minDiff         = [vadNumCh]int16{544, 544, 576, 576, 576, 576}
	maxSpeech       = [vadNumCh]int16{11392, 11392, 11520, 11520, 11520, 11520}
	maxNoise        = [vadNumCh]int16{9216, 9088, 8960, 8832, 8704, 8576}
	minMean         = [vadNumGauss]int16{640, 768}
)

const (
	noiseUpdateConst  = 655  // Q15
	speechUpdateConst = 6554 // Q15
	backEta           = 154  // Q8
)

// WebRTC's mode 3 ("very aggressive") thresholds for 10 ms frames, the one
// mode the sync engine runs.
const (
	mode3Local     = 94
	mode3Global    = 1100
	mode3Overhang1 = 6
	mode3Overhang2 = 9
)

// newVADInst is WebRTC's mode 3 instance with its default adaptation rates.
func newVADInst() *vadInst {
	v := &vadInst{}
	copy(v.noiseMeans[:], initNoiseMeans[:])
	copy(v.speechMeans[:], initSpeechMeans[:])
	copy(v.noiseStds[:], initNoiseStds[:])
	copy(v.speechStds[:], initSpeechStds[:])
	for i := range v.lowValue {
		v.lowValue[i] = 10000
	}
	for i := range v.meanVal {
		v.meanVal[i] = 1600
	}
	v.localThresh = mode3Local
	v.globalThresh = mode3Global
	v.overHangMax1 = mode3Overhang1
	v.overHangMax2 = mode3Overhang2
	v.noiseUpdate = noiseUpdateConst
	v.speechUpdate = speechUpdateConst
	return v
}

// FrameSamples is 10 ms of 8 kHz mono PCM, the frame Detector.LLR reads.
const FrameSamples = 80

// Detector reads one 8 kHz mono stream frame by frame. It is not safe for
// concurrent use.
type Detector struct{ v *vadInst }

// NewDetector returns a mode 3 detector whose model adapts at the smallest
// step the fixed point allows.
func NewDetector() *Detector {
	v := newVADInst()
	// WebRTC's default rates track a film's music and effects into the noise
	// model; the smallest step is the tuning the audio path was measured
	// with. It slows adaptation, it does not stop it.
	v.noiseUpdate, v.speechUpdate = 1, 1
	return &Detector{v: v}
}

// LLR classifies the stream's next frame, which holds FrameSamples samples,
// and returns its summed weighted log-likelihood ratio: the speech evidence
// a Decider thresholds.
func (d *Detector) LLR(frame []int16) float64 {
	_, llr := d.v.processFrameLLR(frame[:FrameSamples])
	return llr
}

// Decider turns one stream's ratios into speech decisions: a ratio at or
// above its threshold is speech, and so are the overhang frames after it.
type Decider struct {
	threshold float64
	overhang  int
	remaining int
}

// NewDecider returns a decider with the given threshold and an overhang of
// that many frames.
func NewDecider(threshold float64, overhang int) *Decider {
	return &Decider{threshold: threshold, overhang: overhang}
}

// Speech reports whether the frame whose ratio is llr is speech; frames are
// passed in stream order.
func (d *Decider) Speech(llr float64) bool {
	if llr >= d.threshold {
		d.remaining = d.overhang
		return true
	}
	if d.remaining > 0 {
		d.remaining--
		return true
	}
	return false
}

// processFrameLLR classifies one frame and returns both the binary decision
// and the raw sum of weighted log-likelihood ratios (continuous confidence).
func (v *vadInst) processFrameLLR(frame []int16) (flag int16, llr float64) {
	features, totalPower := v.extractFeatures(frame)
	return v.gmmProbabilityLLR(features, totalPower)
}
