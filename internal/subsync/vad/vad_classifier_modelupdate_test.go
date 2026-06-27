package vad

import "testing"

// Adaptive model-update reference vectors for gmmProbabilityLLR.
//
// Each test pins the complete GMM model state (or the relevant subset) after
// one classified frame against values captured from the known-correct WebRTC
// port. They are characterization / oracle tests: the GMM model adaptation has
// no simple closed form, so the strongest available check is that the exact
// fixed-point state evolution is preserved. Any change to the adaptation
// arithmetic, branch structure, or clamps shifts at least one pinned value.
//
// The decision-output and overhang tests live in vad_classifier_test.go.

// TestGMMProbabilityLLR_model_noise_frame pins the full model state after a
// non-speech frame: the noise-mean long-term correction with its lo/hi clamps,
// the noise-std update, and the model separation/clamping all run; the speech
// parameters are left unchanged.
func TestGMMProbabilityLLR_model_noise_frame(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	flag, llr := v.gmmProbabilityLLR(featNoise, 100)
	assertGMMState(t, "noise", v, flag, llr,
		0, -608, 1,
		[vadTableSize]int16{8663, 8848, 8769, 8633, 8353, 7725, 8791, 8699, 8897, 8761, 8481, 7869},
		[vadTableSize]int16{10298, 11128, 10666, 12417, 13039, 9500, 11465, 10614, 11467, 8175, 9376, 10674},
		[vadTableSize]int16{384, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{555, 505, 567, 524, 585, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGMMProbabilityLLR_model_speech_frame pins the full model state after a
// speech frame: the speech-mean update with its minMean/max clamps and the
// speech-std update run, while the noise mean still receives its long-term
// correction.
func TestGMMProbabilityLLR_model_speech_frame(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	flag, llr := v.gmmProbabilityLLR(featSpeech, 100)
	assertGMMState(t, "speech", v, flag, llr,
		1, 1470, 1,
		[vadTableSize]int16{8663, 8848, 8768, 8633, 8353, 7724, 8791, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{10298, 11128, 10669, 12417, 13039, 9503, 11463, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{554, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGMMProbabilityLLR_model_noise_uniform_frame pins the model state for a
// uniform low-feature non-speech frame, exercising the noise-mean delta update
// at a different operating point than the realistic noise vector.
func TestGMMProbabilityLLR_model_noise_uniform_frame(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	flag, llr := v.gmmProbabilityLLR(featUniformLow, 100)
	assertGMMState(t, "noiseUniform", v, flag, llr,
		0, 0, 1,
		[vadTableSize]int16{8663, 8848, 8769, 8633, 8353, 7723, 8791, 8699, 8897, 8761, 8481, 7869},
		[vadTableSize]int16{10298, 11128, 10666, 12417, 13039, 9500, 11465, 10614, 11467, 8175, 9376, 10674},
		[vadTableSize]int16{384, 1064, 491, 585, 690, 594, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{555, 505, 567, 524, 585, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGMMProbabilityLLR_model_noise_mean_low_clamp drives a speech frame with
// channel-0 gaussian-0 positioned so its corrected noise mean lands just below
// the lower bound lo = (k+5)<<7, making the lower clamp decisive.
func TestGMMProbabilityLLR_model_noise_mean_low_clamp(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	v.globalThresh = -32768 // force vadflag == 1 (sumLLR >= globalThresh always)
	v.noiseMeans[0] = 0     // nmk2 for channel 0, gaussian 0
	v.noiseMeans[6] = 16000 // raises nGlobalQ8 so the long-term correction is small
	flag, llr := v.gmmProbabilityLLR(featSpeech, 100)
	assertGMMState(t, "loclamp", v, flag, llr,
		1, 1614, 1,
		[vadTableSize]int16{607, 8848, 8768, 8633, 8353, 7724, 9311, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{8449, 11128, 10669, 12417, 13039, 9503, 9614, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{554, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGMMProbabilityLLR_model_speech_mean_high_clamp drives a speech frame with
// a high initial speech mean so the updated value lands above the upper bound
// mx = maxSpeechMean + 640, making the upper clamp decisive.
func TestGMMProbabilityLLR_model_speech_mean_high_clamp(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	v.globalThresh = -32768
	v.speechMeans[0] = 13000
	flag, llr := v.gmmProbabilityLLR(featSpeech, 100)
	assertGMMState(t, "mxclamp", v, flag, llr,
		1, 1452, 1,
		[vadTableSize]int16{8991, 8848, 8768, 8633, 8353, 7724, 9119, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{13568, 11128, 10669, 12417, 13039, 9503, 10026, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{555, 504, 567, 523, 584, 1231, 512, 828, 492, 1540, 1079, 850},
	)
}

// TestGMMProbabilityLLR_model_speech_std_mean_shift drives a speech frame with
// channel-0 speech parameters tuned so the speech-std update's smoothed-mean
// term (smk+4)>>3 propagates a one-LSB difference into the stored std.
func TestGMMProbabilityLLR_model_speech_std_mean_shift(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	v.globalThresh = -32768
	v.speechMeans[0] = 500
	v.speechStds[0] = 800
	f := featSpeech // array copy; the package var is not mutated
	f[0] = 50
	flag, llr := v.gmmProbabilityLLR(f, 100)
	assertGMMState(t, "stdshift", v, flag, llr,
		1, 1608, 1,
		[vadTableSize]int16{8124, 8848, 8768, 8633, 8353, 7724, 8252, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{4965, 11128, 10669, 12417, 13039, 9503, 13798, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{800, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGMMProbabilityLLR_model_noise_std_at_floor forces a non-speech frame with
// all noise stds at the vadMinStd floor and quiet features, so the noise-std
// update lifts several stds just above the floor (385, 386). The spread covers
// both signs of the rounding division in the std update.
func TestGMMProbabilityLLR_model_noise_std_at_floor(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767 // suppress the global-sum trigger -> vadflag 0
	v.localThresh = 32767  // suppress the per-channel trigger   -> vadflag 0
	setAllGMM(&v.noiseStds, vadMinStd)
	flag, _ := v.gmmProbabilityLLR(featUniformLow, 100)
	assertGMMStds(t, "noiseStdLow", flag, 0, v.frameCounter, 1, v.noiseStds,
		[vadTableSize]int16{385, 384, 386, 384, 385, 386, 384, 384, 384, 384, 384, 384})
}

// TestGMMProbabilityLLR_model_noise_std_prob_bias forces a non-speech frame
// with zeroed noise means and a large feature offset, so the conditional
// probability term is large enough that its +2 rounding bias propagates into a
// stored std.
func TestGMMProbabilityLLR_model_noise_std_prob_bias(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767
	v.localThresh = 32767
	setAllGMM(&v.noiseStds, 400)
	setAllGMM(&v.noiseMeans, 0)
	flag, _ := v.gmmProbabilityLLR(featUniformMid, 100)
	assertGMMStds(t, "noiseStdBias", flag, 0, v.frameCounter, 1, v.noiseStds,
		[vadTableSize]int16{398, 398, 400, 404, 405, 403, 402, 402, 401, 396, 395, 398})
}

// TestGMMProbabilityLLR_model_separation_and_noise_clamp drives the model
// separation block (the speech/noise means are too close, so they are pushed
// apart) and the global-noise-mean upper clamp, both firing on the same frame.
func TestGMMProbabilityLLR_model_separation_and_noise_clamp(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767 // suppress the global-sum trigger -> vadflag 0
	v.localThresh = 32767  // suppress the per-channel trigger -> vadflag 0
	setAllGMM(&v.noiseMeans, 12000)
	setAllGMM(&v.speechMeans, 8000)
	flag, llr := v.gmmProbabilityLLR(featUniform1000, 100)
	assertGMMMeans(t, "sep", flag, llr, v.frameCounter, v.noiseMeans, v.speechMeans,
		0, 1546, 1,
		[vadTableSize]int16{8469, 8398, 8282, 8171, 8052, 7915, 8597, 8526, 8410, 8299, 8180, 8043},
		[vadTableSize]int16{10830, 10704, 10697, 10596, 10502, 10421, 10830, 10704, 10697, 10596, 10502, 10421},
	)
}

// TestGMMProbabilityLLR_model_speech_mean_clamp drives the global-speech-mean
// upper clamp: the speech means start above maxSpeech[ch], so each is pulled
// down to exactly its per-channel bound, with the separation block not firing.
func TestGMMProbabilityLLR_model_speech_mean_clamp(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767
	v.localThresh = 32767
	setAllGMM(&v.speechMeans, 14000)
	setAllGMM(&v.noiseMeans, 8000)
	flag, llr := v.gmmProbabilityLLR(featUniform1000, 100)
	assertGMMMeans(t, "speechClamp", flag, llr, v.frameCounter, v.noiseMeans, v.speechMeans,
		0, -1696, 1,
		[vadTableSize]int16{9122, 9022, 8904, 8770, 8629, 8473, 9250, 9150, 9032, 8898, 8757, 8601},
		[vadTableSize]int16{11392, 11392, 11520, 11520, 11520, 11520, 11392, 11392, 11520, 11520, 11520, 11520},
	)
}
