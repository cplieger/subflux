package vad

// Filter bank functions ported from vad_filterbank.c (WebRTC).

func vadAllpass(in []int16, stride, n int, coef int16, state *int16, out []int16) {
	st := int32(*state) << 16
	for i := range n {
		tmp32 := st + int32(coef)*int32(in[i*stride])
		out[i] = int16(tmp32 >> 16) //nolint:gosec // G115: fixed-point DSP
		st = (int32(in[i*stride]) << 14) - int32(coef)*int32(out[i])
		st *= 2
	}
	*state = int16(st >> 16) //nolint:gosec // G115: fixed-point DSP
}

const (
	allpassUpper int16 = 20972 // 0.64 in Q15
	allpassLower int16 = 5571  // 0.17 in Q15
)

func vadSplit(in []int16, n int, upper, lower *int16, hp, lp []int16) {
	half := n / 2
	vadAllpass(in[0:], 2, half, allpassUpper, upper, hp)
	vadAllpass(in[1:], 2, half, allpassLower, lower, lp)
	for i := range half {
		tmp := hp[i]
		hp[i] -= lp[i]
		lp[i] += tmp
	}
}

var (
	hpZero = [3]int16{6631, -13262, 6631}
	hpPole = [3]int16{16384, -7756, 5620}
)

func vadHP(in []int16, n int, state, out []int16) {
	for i := range n {
		tmp32 := int32(hpZero[0]) * int32(in[i])
		tmp32 += int32(hpZero[1]) * int32(state[0])
		tmp32 += int32(hpZero[2]) * int32(state[1])
		state[1] = state[0]
		state[0] = in[i]
		tmp32 -= int32(hpPole[1]) * int32(state[2])
		tmp32 -= int32(hpPole[2]) * int32(state[3])
		state[3] = state[2]
		state[2] = int16(tmp32 >> 14) //nolint:gosec // G115: fixed-point DSP
		out[i] = state[2]
	}
}

// vadLogEnergy: exact port of LogOfEnergy.
func vadLogEnergy(data []int16, n int, offset int16) (logE, totalE int16) {
	var energy uint32
	var rshifts int
	for i := range n {
		sq := uint32(int32(data[i]) * int32(data[i])) //nolint:gosec // G115: fixed-point DSP arithmetic
		if energy > 0xFFFFFFFF-sq {
			energy >>= 1
			energy += sq >> 1
			rshifts++
		} else {
			energy += sq
		}
	}
	if energy == 0 {
		return offset, 0
	}
	// NormU32: count leading zeros.
	normU := 0
	t := energy
	for t < 0x80000000 {
		t <<= 1
		normU++
	}
	normR := 17 - normU
	rshifts += normR
	if normR < 0 {
		energy <<= uint(-normR)
	} else {
		energy >>= uint(normR)
	}
	const kLogIntPart int16 = 14 << 10
	const kLogConst int32 = 24660
	log2e := kLogIntPart + int16((energy&0x3FFF)>>4)
	logE = int16((kLogConst*int32(log2e))>>19 + (int32(rshifts)*kLogConst)>>9)
	logE = max(logE, 0)
	logE += offset
	if totalE <= vadMinEnergy {
		if rshifts >= 0 {
			totalE = vadMinEnergy + 1
		} else {
			totalE = int16(energy >> uint(-rshifts)) //nolint:gosec // G115: fixed-point DSP arithmetic
		}
	}
	return logE, totalE
}

// extractFeatures: exact port of WebRtcVad_CalculateFeatures.
func (v *vadInst) extractFeatures(frame []int16) (features [vadNumCh]int16, totalE int16) {
	var hp120, lp120 [60]int16
	var hp60, lp60 [30]int16

	// [0-4000] → [2000-4000] + [0-2000]
	vadSplit(frame, 80, &v.upperState[0], &v.lowerState[0], hp120[:], lp120[:])
	// [2000-4000] → [3000-4000] + [2000-3000]
	vadSplit(hp120[:40], 40, &v.upperState[1], &v.lowerState[1], hp60[:], lp60[:])
	features[5], totalE = vadLogEnergy(hp60[:], 20, offsets[5])
	features[4], _ = vadLogEnergy(lp60[:], 20, offsets[4])
	// [0-2000] → [1000-2000] + [0-1000]
	vadSplit(lp120[:40], 40, &v.upperState[2], &v.lowerState[2], hp60[:], lp60[:])
	features[3], _ = vadLogEnergy(hp60[:], 20, offsets[3])
	// [0-1000] → [500-1000] + [0-500]
	vadSplit(lp60[:20], 20, &v.upperState[3], &v.lowerState[3], hp120[:], lp120[:])
	features[2], _ = vadLogEnergy(hp120[:], 10, offsets[2])
	// [0-500] → [250-500] + [0-250]
	vadSplit(lp120[:10], 10, &v.upperState[4], &v.lowerState[4], hp60[:], lp60[:])
	features[1], _ = vadLogEnergy(hp60[:], 5, offsets[1])
	// HP filter 80Hz on [0-250]
	var hpOut [5]int16
	vadHP(lp60[:5], 5, v.hpState[:], hpOut[:])
	features[0], _ = vadLogEnergy(hpOut[:], 5, offsets[0])
	return features, totalE
}
