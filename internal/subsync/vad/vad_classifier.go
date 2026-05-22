package vad

// GMM classifier functions ported from vad_core.c (WebRTC).

// vadGaussProb: exact port of WebRtcVad_GaussianProbability.
// Precondition: std > 0 (enforced by vadMinStd clamp in model update).
func vadGaussProb(input, mean, std int16) (prob int32, delta int16) {
	tmp32 := int32(131072) + int32(std>>1)
	invStd := int16(tmp32 / int32(std))                  // Q10
	tmp16 := invStd >> 2                                 // Q8
	invStd2 := int16((int32(tmp16) * int32(tmp16)) >> 2) // Q14
	xQ7 := input << 3
	d := xQ7 - mean                                  // Q7
	delta = int16((int32(invStd2) * int32(d)) >> 10) // Q11
	expArg := (int32(delta) * int32(d)) >> 9         // Q10

	const kCompVar = 22005 // Maximum allowed exponent argument (prevents underflow).
	const kLog2Exp = 5909  // log2(e) in Q12, used for exp(-x) approximation.
	var expVal int16
	if expArg < kCompVar {
		t := int16((int64(kLog2Exp) * int64(expArg)) >> 12)
		t = -t
		expVal = 0x0400 | (t & 0x03FF)
		t = ^t
		t >>= 10
		t += 1
		if t >= 0 && t < 16 {
			expVal >>= uint(t)
		} else {
			expVal = 0
		}
	}
	prob = int32(invStd) * int32(expVal) // Q20
	return
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

//nolint:gocyclo // numerical algorithm ported from WebRTC with inherent branching
func (v *vadInst) gmmProbabilityLLR(features [vadNumCh]int16, totalPower int16) (flag int16, llr float64) {
	var vadflag int16
	var sumLLR int32
	var deltaN, deltaS [vadTableSize]int16
	var ngprvec, sgprvec [vadTableSize]int16

	if totalPower > v.minEnergy {
		// --- GMM likelihood computation ---
		for ch := range vadNumCh {
			var h0test, h1test int32
			var nProb, sProb [vadNumGauss]int32

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
				vadflag = 1
			}
			sumLLR += int32(llr) * int32(v.specWeights[ch])

			// Conditional probabilities for model update (Q14: 16384 = 1.0).
			h0 := int16(h0test >> 12)
			if h0 > 0 {
				ngprvec[ch] = int16((nProb[0] & ^int32(0xFFF)) << 2 / int32(h0))
				ngprvec[ch+vadNumCh] = 16384 - ngprvec[ch]
			} else {
				ngprvec[ch] = 16384 // All probability on Gaussian 0.
			}
			h1 := int16(h1test >> 12)
			if h1 > 0 {
				sgprvec[ch] = int16((sProb[0] & ^int32(0xFFF)) << 2 / int32(h1))
				sgprvec[ch+vadNumCh] = 16384 - sgprvec[ch]
			}
		}

		if sumLLR >= int32(v.globalThresh) {
			vadflag |= 1
		}

		// --- Adaptive model update ---
		maxspe := int16(12800) // Upper bound for speech mean update (from vad_core.c).
		for ch := range vadNumCh {
			featureMin := v.findMinimum(features[ch], ch)

			// Global noise mean.
			var noiseGlobal int32
			for k := range vadNumGauss {
				g := ch + k*vadNumCh
				noiseGlobal += int32(v.noiseMeans[g]) * int32(noiseWt[g])
			}
			nGlobalQ8 := int16(noiseGlobal >> 6)

			for k := range vadNumGauss {
				g := ch + k*vadNumCh
				nmk := v.noiseMeans[g]
				smk := v.speechMeans[g]
				nsk := v.noiseStds[g]
				ssk := v.speechStds[g]

				// Update noise mean if non-speech.
				nmk2 := nmk
				if vadflag == 0 {
					delt := int16((int32(ngprvec[g]) * int32(deltaN[g])) >> 11)
					nmk2 = nmk + int16((int32(delt)*int32(v.noiseUpdate))>>22)
				}

				// Long-term noise correction.
				ndelt := (featureMin << 4) - nGlobalQ8
				nmk3 := nmk2 + int16((int32(ndelt)*backEta)>>9)

				lo := int16((k + 5) << 7)
				if nmk3 < lo {
					nmk3 = lo
				}
				hi := int16((72 + k - ch) << 7)
				if nmk3 > hi {
					nmk3 = hi
				}
				v.noiseMeans[g] = nmk3

				if vadflag != 0 {
					// Update speech mean.
					delt := int16((int32(sgprvec[g]) * int32(deltaS[g])) >> 11)
					tmp := int16((int32(delt) * int32(v.speechUpdate)) >> 21)
					smk2 := smk + ((tmp + 1) >> 1)
					mx := maxspe + 640
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
						tmp = int16(tmp2 / int32(ssk*10))
					} else {
						tmp = -int16(-tmp2 / int32(ssk*10))
					}
					tmp += 128
					ssk += tmp >> 8
					if ssk < vadMinStd {
						ssk = vadMinStd
					}
					v.speechStds[g] = ssk
				} else {
					// Update noise std.
					tmp := features[ch] - (nmk >> 3)
					tmp1 := (int32(deltaN[g]) * int32(tmp)) >> 3
					tmp1 -= 4096
					tmpQ := (ngprvec[g] + 2) >> 2
					tmp2 := int32(tmpQ) * tmp1
					tmp1 = tmp2 >> 14
					if tmp1 > 0 {
						tmp = int16(tmp1 / int32(nsk))
					} else {
						tmp = -int16(-tmp1 / int32(nsk))
					}
					tmp += 32
					nsk += tmp >> 6
					if nsk < vadMinStd {
						nsk = vadMinStd
					}
					v.noiseStds[g] = nsk
				}
			}

			// --- Model separation and clamping ---
			var nGlob, sGlob int32
			for k := range vadNumGauss {
				g := ch + k*vadNumCh
				nGlob += int32(v.noiseMeans[g]) * int32(noiseWt[g])
				sGlob += int32(v.speechMeans[g]) * int32(speechWt[g])
			}
			diff := int16(sGlob>>9) - int16(nGlob>>9)
			if diff < minDiff[ch] {
				gap := minDiff[ch] - diff
				sShift := int16((13 * int32(gap)) >> 2)
				nShift := int16((3 * int32(gap)) >> 2)
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
		v.frameCounter++
	}

	// --- Overhang smoothing ---
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
	return vadflag, float64(sumLLR)
}
