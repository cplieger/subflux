// vad.go is a line-by-line pure Go port of WebRTC's Voice Activity Detector.
//
// Original: chromium/webrtc common_audio/vad/ (BSD-3-Clause).
// Copyright (c) 2012 The WebRTC project authors. All Rights Reserved.
//
// Includes: allpass filter bank, log energy features, GMM classifier,
// and adaptive model update. No cgo, no external dependencies.

package vad

import "context"

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

	// Adaptation speed (Q15). Default: noise=655, speech=6554.
	noiseUpdate  int16
	speechUpdate int16

	// Configurable parameters.
	specWeights [vadNumCh]int16
	minEnergy   int16

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

// Mode represents the VAD aggressiveness level (WebRTC modes 0-3).
type Mode int

const (
	ModeQuality        Mode = 0 // Most permissive, fewest false negatives
	ModeLowBitrate     Mode = 1 // Low bitrate
	ModeAggressive     Mode = 2 // Aggressive
	ModeVeryAggressive Mode = 3 // Most restrictive, fewest false positives
)

// Mode thresholds for 10ms frames (original WebRTC values).
// [local, global, overhang1, overhang2]
var vadModes = [4]struct{ local, global, oh1, oh2 int16 }{
	{24, 57, 8, 14},  // Mode 0: Quality
	{37, 100, 8, 14}, // Mode 1: Low bitrate
	{82, 285, 6, 9},  // Mode 2: Aggressive
	{94, 1100, 6, 9}, // Mode 3: Very aggressive
}

func newVADInst(mode Mode) *vadInst {
	if mode < 0 || int(mode) >= len(vadModes) {
		mode = ModeVeryAggressive
	}
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
	m := vadModes[int(mode)]
	v.localThresh = m.local
	v.globalThresh = m.global
	v.overHangMax1 = m.oh1
	v.overHangMax2 = m.oh2
	v.noiseUpdate = noiseUpdateConst
	v.speechUpdate = speechUpdateConst
	v.specWeights = specWeight
	v.minEnergy = vadMinEnergy
	return v
}

// newVADInstAdapt creates a VAD instance with custom adaptation speed.
// adaptScale multiplies the default update constants (1.0 = default,
// 2.0 = twice as fast, 0.5 = half speed).
func newVADInstAdapt(mode Mode, adaptScale float64) *vadInst {
	v := newVADInst(mode)
	// Clamp to prevent int16 overflow.
	// speechUpdateConst * 5.0 = 32770, which overflows int16 by 3.
	// The max safe scale is 32767/speechUpdateConst ≈ 4.9995.
	maxScale := float64(32767) / float64(speechUpdateConst)
	adaptScale = min(max(adaptScale, 0), maxScale)
	v.noiseUpdate = int16(float64(noiseUpdateConst) * adaptScale)
	v.speechUpdate = int16(float64(speechUpdateConst) * adaptScale)
	if v.noiseUpdate < 1 {
		v.noiseUpdate = 1
	}
	if v.speechUpdate < 1 {
		v.speechUpdate = 1
	}
	return v
}

// Tuning holds optional GMM tuning parameters.
type Tuning struct {
	specWeights *[vadNumCh]int16 // nil = use default
	minEnergy   *int16           // nil = use default
}

// newVADInstTuned creates a VAD instance with custom tuning.
func newVADInstTuned(mode Mode, adaptScale float64, tuning Tuning) *vadInst {
	v := newVADInstAdapt(mode, adaptScale)
	if tuning.specWeights != nil {
		v.specWeights = *tuning.specWeights
	}
	if tuning.minEnergy != nil {
		v.minEnergy = *tuning.minEnergy
	}
	return v
}

// FramesBinaryThresholdTuned returns binary {-1, +1} with custom threshold,
// overhang, adaptation, and GMM tuning parameters. PCM must be 8kHz mono s16le.
func FramesBinaryThresholdTuned(ctx context.Context, pcm []int16, mode Mode, threshold float64, overhangFrames int, adaptScale float64, tuning Tuning) []float64 {
	const frameSize = 80 // 10ms at 8kHz
	// Trailing samples that don't fill a complete 80-sample frame are ignored.
	nFrames := len(pcm) / frameSize
	result := make([]float64, nFrames)
	v := newVADInstTuned(mode, adaptScale, tuning)
	remaining := 0
	for i := range nFrames {
		// Check for cancellation every 1000 frames (amortized cost: negligible).
		if i%1000 == 0 && ctx.Err() != nil {
			return result[:i]
		}
		frame := pcm[i*frameSize : (i+1)*frameSize]
		_, llr := v.processFrameLLR(frame)
		switch {
		case llr >= threshold:
			result[i] = 1.0
			remaining = overhangFrames
		case remaining > 0:
			result[i] = 1.0
			remaining--
		default:
			result[i] = -1.0
		}
	}
	return result
}

// processFrameLLR classifies one frame and returns both the binary decision
// and the raw sum of weighted log-likelihood ratios (continuous confidence).
func (v *vadInst) processFrameLLR(frame []int16) (flag int16, llr float64) {
	features, totalPower := v.extractFeatures(frame)
	return v.gmmProbabilityLLR(features, totalPower)
}
