package vad

// GMM classifier functions ported from vad_core.c (WebRTC).

// maxSpeechMean is the upper bound for the speech-mean update (from vad_core.c).
const maxSpeechMean int16 = 12800

// vadGaussProb: exact port of WebRtcVad_GaussianProbability.
// Precondition: std > 0 (enforced by vadMinStd clamp in model update).
func vadGaussProb(input, mean, std int16) (prob int32, delta int16) {
	tmp32 := int32(131072) + int32(std>>1)
	invStd := int16(tmp32 / int32(std))                  //nolint:gosec // G115: fixed-point DSP, Q10
	tmp16 := invStd >> 2                                 // Q8
	invStd2 := int16((int32(tmp16) * int32(tmp16)) >> 2) //nolint:gosec // G115: fixed-point DSP, Q14
	xQ7 := input << 3
	d := xQ7 - mean                                  // Q7
	delta = int16((int32(invStd2) * int32(d)) >> 10) //nolint:gosec // G115: fixed-point DSP, Q11
	expArg := (int32(delta) * int32(d)) >> 9         // Q10

	const kCompVar = 22005 // Maximum allowed exponent argument (prevents underflow).
	const kLog2Exp = 5909  // log2(e) in Q12, used for exp(-x) approximation.
	var expVal int16
	if expArg < kCompVar {
		t := int16((int64(kLog2Exp) * int64(expArg)) >> 12) //nolint:gosec // G115: fixed-point DSP arithmetic
		t = -t
		expVal = 0x0400 | (t & 0x03FF)
		t = ^t
		t >>= 10
		t++
		if t >= 0 && t < 16 {
			expVal >>= uint(t)
		} else {
			expVal = 0
		}
	}
	prob = int32(invStd) * int32(expVal) // Q20
	return prob, delta
}

// vadNormW32 counts leading zeros up to bit 30 (WebRTC NormW32 equivalent).
func vadNormW32(val int32) int32 {
	if val <= 0 {
		return 0
	}
	var n int32
	for val < (1 << 30) {
		val <<= 1
		n++
	}
	return n
}

// gmmProbabilityLLR runs the WebRTC GMM voice-activity classifier for one
// frame: it computes the per-channel log-likelihood ratios (returning the
// binary speech flag and the summed weighted LLR), then adapts the GMM model
// and applies overhang smoothing. The numerical work is delegated to verbatim
// per-phase helpers so the fixed-point arithmetic order is preserved exactly.
func (v *vadInst) gmmProbabilityLLR(features [vadNumCh]int16, totalPower int16) (flag int16, llr float64) {
	var vadflag int16
	var sumLLR int32
	var deltaN, deltaS [vadTableSize]int16
	var ngprvec, sgprvec [vadTableSize]int16

	if totalPower > v.minEnergy {
		// --- GMM likelihood computation ---
		for ch := range vadNumCh {
			llrContrib, localFlag := v.channelLLR(ch, features, &deltaN, &deltaS, &ngprvec, &sgprvec)
			if localFlag != 0 {
				vadflag = 1
			}
			sumLLR += llrContrib
		}

		if sumLLR >= int32(v.globalThresh) {
			vadflag |= 1
		}

		// --- Adaptive model update ---
		for ch := range vadNumCh {
			v.updateChannelModel(ch, vadflag, features, &deltaN, &deltaS, &ngprvec, &sgprvec)
		}
		v.frameCounter++
	}

	return v.applyOverhang(vadflag), float64(sumLLR)
}

// gaussLikelihoods evaluates both Gaussians of every mixture for one channel,
// returning the noise/speech likelihood sums (h0test, h1test) and the weighted
// per-Gaussian probabilities; it records each Gaussian's delta in deltaN/deltaS.
func (v *vadInst) gaussLikelihoods(ch int, features [vadNumCh]int16, deltaN, deltaS *[vadTableSize]int16) (h0test, h1test int32, nProb, sProb [vadNumGauss]int32) {
	for k := range vadNumGauss {
		g := ch + k*vadNumCh
		var nd, sd int16
		nProb[k], nd = vadGaussProb(features[ch], v.noiseMeans[g], v.noiseStds[g])
		deltaN[g] = nd
		nProb[k] = int32(noiseWt[g]) * nProb[k]
		h0test += nProb[k]

		sProb[k], sd = vadGaussProb(features[ch], v.speechMeans[g], v.speechStds[g])
		deltaS[g] = sd
		sProb[k] = int32(speechWt[g]) * sProb[k]
		h1test += sProb[k]
	}
	return h0test, h1test, nProb, sProb
}

// channelLLR computes one channel's weighted log-likelihood-ratio contribution
// to the global sum, reports whether the channel alone trips the local speech
// threshold, and fills the per-Gaussian conditional probabilities used by the
// model update.
func (v *vadInst) channelLLR(ch int, features [vadNumCh]int16, deltaN, deltaS, ngprvec, sgprvec *[vadTableSize]int16) (llrContrib int32, localFlag int16) {
	h0test, h1test, nProb, sProb := v.gaussLikelihoods(ch, features, deltaN, deltaS)

	sh0 := vadNormW32(h0test)
	sh1 := vadNormW32(h1test)
	if h0test == 0 {
		sh0 = 31
	}
	if h1test == 0 {
		sh1 = 31
	}
	llr := int16(sh0 - sh1)

	if llr*4 > v.localThresh {
		localFlag = 1
	}
	llrContrib = int32(llr) * int32(v.specWeights[ch])

	// Conditional probabilities for model update (Q14: 16384 = 1.0).
	h0 := int16(h0test >> 12) //nolint:gosec // G115: fixed-point DSP
	if h0 > 0 {
		ngprvec[ch] = int16((nProb[0] & ^int32(0xFFF)) << 2 / int32(h0)) //nolint:gosec // G115: fixed-point DSP
		ngprvec[ch+vadNumCh] = 16384 - ngprvec[ch]
	} else {
		ngprvec[ch] = 16384 // All probability on Gaussian 0.
	}
	h1 := int16(h1test >> 12) //nolint:gosec // G115: fixed-point DSP
	if h1 > 0 {
		sgprvec[ch] = int16((sProb[0] & ^int32(0xFFF)) << 2 / int32(h1)) //nolint:gosec // G115: fixed-point DSP
		sgprvec[ch+vadNumCh] = 16384 - sgprvec[ch]
	}
	return llrContrib, localFlag
}

// updateChannelModel adapts one channel's GMM parameters for the current frame:
// it refreshes the long-term feature minimum, updates each Gaussian's mean and
// standard deviation, then enforces the model-separation and clamping bounds.
func (v *vadInst) updateChannelModel(ch int, vadflag int16, features [vadNumCh]int16, deltaN, deltaS, ngprvec, sgprvec *[vadTableSize]int16) {
	featureMin := v.findMinimum(features[ch], ch)
	nGlobalQ8 := v.noiseMeanGlobalQ8(ch)
	for k := range vadNumGauss {
		v.updateGaussian(ch, k, vadflag, features, featureMin, nGlobalQ8, deltaN, deltaS, ngprvec, sgprvec)
	}
	v.separateAndClampMeans(ch)
}

// noiseMeanGlobalQ8 returns the weighted global noise mean for a channel in Q8.
func (v *vadInst) noiseMeanGlobalQ8(ch int) int16 {
	var noiseGlobal int32
	for k := range vadNumGauss {
		g := ch + k*vadNumCh
		noiseGlobal += int32(v.noiseMeans[g]) * int32(noiseWt[g])
	}
	return int16(noiseGlobal >> 6)
}

// updateGaussian updates the noise mean for one (channel, Gaussian) with the
// long-term correction and lo/hi clamps, then dispatches to the speech- or
// noise-parameter update depending on the frame's speech flag.
func (v *vadInst) updateGaussian(ch, k int, vadflag int16, features [vadNumCh]int16, featureMin, nGlobalQ8 int16, deltaN, deltaS, ngprvec, sgprvec *[vadTableSize]int16) {
	g := ch + k*vadNumCh
	nmk := v.noiseMeans[g]
	smk := v.speechMeans[g]
	nsk := v.noiseStds[g]
	ssk := v.speechStds[g]

	// Update noise mean if non-speech.
	nmk2 := nmk
	if vadflag == 0 {
		delt := int16((int32(ngprvec[g]) * int32(deltaN[g])) >> 11) //nolint:gosec // G115: fixed-point DSP
		nmk2 = nmk + int16((int32(delt)*int32(v.noiseUpdate))>>22)  //nolint:gosec // G115: fixed-point DSP
	}

	// Long-term noise correction.
	ndelt := (featureMin << 4) - nGlobalQ8
	nmk3 := nmk2 + int16((int32(ndelt)*backEta)>>9) //nolint:gosec // G115: fixed-point DSP

	lo := int16((k + 5) << 7) //nolint:gosec // G115: fixed-point DSP
	if nmk3 < lo {
		nmk3 = lo
	}
	hi := int16((72 + k - ch) << 7) //nolint:gosec // G115: fixed-point DSP
	if nmk3 > hi {
		nmk3 = hi
	}
	v.noiseMeans[g] = nmk3

	if vadflag != 0 {
		v.updateSpeechParams(ch, k, g, features, smk, ssk, deltaS, sgprvec)
	} else {
		v.updateNoiseStd(ch, g, features, nmk, nsk, deltaN, ngprvec)
	}
}

// updateSpeechParams updates the speech mean (with its minMean/max clamps) and
// the speech standard deviation for one (channel, Gaussian) on a speech frame.
func (v *vadInst) updateSpeechParams(ch, k, g int, features [vadNumCh]int16, smk, ssk int16, deltaS, sgprvec *[vadTableSize]int16) {
	// Update speech mean.
	delt := int16((int32(sgprvec[g]) * int32(deltaS[g])) >> 11) //nolint:gosec // G115: fixed-point DSP
	tmp := int16((int32(delt) * int32(v.speechUpdate)) >> 21)   //nolint:gosec // G115: fixed-point DSP
	smk2 := smk + ((tmp + 1) >> 1)
	mx := maxSpeechMean + 640
	if smk2 < minMean[k] {
		smk2 = minMean[k]
	}
	if smk2 > mx {
		smk2 = mx
	}
	v.speechMeans[g] = smk2

	// Update speech std.
	tmp = ((smk + 4) >> 3)
	tmp = features[ch] - tmp
	tmp1 := (int32(deltaS[g]) * int32(tmp)) >> 3
	tmp2 := tmp1 - 4096
	tmpQ := sgprvec[g] >> 2
	tmp1 = int32(tmpQ) * tmp2
	tmp2 = tmp1 >> 4
	if tmp2 > 0 {
		tmp = int16(tmp2 / int32(ssk*10)) //nolint:gosec // G115: fixed-point DSP
	} else {
		tmp = -int16(-tmp2 / int32(ssk*10)) //nolint:gosec // G115: fixed-point DSP
	}
	tmp += 128
	ssk += tmp >> 8
	if ssk < vadMinStd {
		ssk = vadMinStd
	}
	v.speechStds[g] = ssk
}

// updateNoiseStd updates the noise standard deviation for one (channel,
// Gaussian) on a non-speech frame, clamped at the vadMinStd floor.
func (v *vadInst) updateNoiseStd(ch, g int, features [vadNumCh]int16, nmk, nsk int16, deltaN, ngprvec *[vadTableSize]int16) {
	tmp := features[ch] - (nmk >> 3)
	tmp1 := (int32(deltaN[g]) * int32(tmp)) >> 3
	tmp1 -= 4096
	tmpQ := (ngprvec[g] + 2) >> 2
	tmp2 := int32(tmpQ) * tmp1
	tmp1 = tmp2 >> 14
	if tmp1 > 0 {
		tmp = int16(tmp1 / int32(nsk)) //nolint:gosec // G115: fixed-point DSP
	} else {
		tmp = -int16(-tmp1 / int32(nsk)) //nolint:gosec // G115: fixed-point DSP
	}
	tmp += 32
	nsk += tmp >> 6
	if nsk < vadMinStd {
		nsk = vadMinStd
	}
	v.noiseStds[g] = nsk
}

// separateAndClampMeans enforces the minimum noise/speech model separation and
// the per-channel upper bounds on the global noise and speech means.
func (v *vadInst) separateAndClampMeans(ch int) {
	var nGlob, sGlob int32
	for k := range vadNumGauss {
		g := ch + k*vadNumCh
		nGlob += int32(v.noiseMeans[g]) * int32(noiseWt[g])
		sGlob += int32(v.speechMeans[g]) * int32(speechWt[g])
	}
	diff := int16(sGlob>>9) - int16(nGlob>>9)
	if diff < minDiff[ch] {
		gap := minDiff[ch] - diff
		sShift := int16((13 * int32(gap)) >> 2) //nolint:gosec // G115: fixed-point DSP
		nShift := int16((3 * int32(gap)) >> 2)  //nolint:gosec // G115: fixed-point DSP
		for k := range vadNumGauss {
			v.speechMeans[ch+k*vadNumCh] += sShift
			v.noiseMeans[ch+k*vadNumCh] -= nShift
		}
	}

	// Clamp means.
	maxs := maxSpeech[ch]
	sG := int16(sGlob >> 7)
	if sG > maxs {
		d := sG - maxs
		for k := range vadNumGauss {
			v.speechMeans[ch+k*vadNumCh] -= d
		}
	}
	nG := int16(nGlob >> 7)
	if nG > maxNoise[ch] {
		d := nG - maxNoise[ch]
		for k := range vadNumGauss {
			v.noiseMeans[ch+k*vadNumCh] -= d
		}
	}
}

// applyOverhang implements the speech hangover state machine: it extends the
// speech decision for a few frames after speech ends and arms the overhang
// length based on how long speech has been sustained.
func (v *vadInst) applyOverhang(vadflag int16) int16 {
	if vadflag == 0 {
		if v.overHang > 0 {
			vadflag = 2 + v.overHang
			v.overHang--
		}
		v.numOfSpeech = 0
	} else {
		v.numOfSpeech++
		if v.numOfSpeech > vadMaxSpeechFr {
			v.numOfSpeech = vadMaxSpeechFr
			v.overHang = v.overHangMax2
		} else {
			v.overHang = v.overHangMax1
		}
	}
	return vadflag
}
