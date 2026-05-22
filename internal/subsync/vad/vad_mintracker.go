package vad

// Minimum value tracker ported from vad_core.c (WebRTC).

// findMinimum: exact port of WebRtcVad_FindMinimum.
// Maintains a sorted list of the 16 smallest values over the last 100 frames,
// returns the smoothed median of the 5 smallest.
//
//nolint:gocyclo // numerical algorithm ported from WebRTC with inherent branching
func (v *vadInst) findMinimum(featureVal int16, ch int) int16 {
	const (
		smoothDown int16 = 6553  // 0.2 in Q15
		smoothUp   int16 = 32439 // 0.99 in Q15
	)

	off := ch << 4 // 16 values per channel
	age := v.indexVec[off : off+16]
	sv := v.lowValue[off : off+16]

	// Age all values; remove those older than 100 frames.
	for i := range 16 {
		if age[i] != 100 {
			age[i]++
		} else {
			for j := i; j < 15; j++ {
				sv[j] = sv[j+1]
				age[j] = age[j+1]
			}
			age[15] = 101
			sv[15] = 10000
		}
	}

	// Binary search for insertion position.
	pos := -1
	//nolint:gocritic // ifElseChain: binary search tree is a faithful port of WebRTC C code
	if featureVal < sv[7] {
		if featureVal < sv[3] {
			if featureVal < sv[1] {
				if featureVal < sv[0] {
					pos = 0
				} else {
					pos = 1
				}
			} else if featureVal < sv[2] {
				pos = 2
			} else {
				pos = 3
			}
		} else if featureVal < sv[5] {
			if featureVal < sv[4] {
				pos = 4
			} else {
				pos = 5
			}
		} else if featureVal < sv[6] {
			pos = 6
		} else {
			pos = 7
		}
	} else if featureVal < sv[15] {
		if featureVal < sv[11] {
			if featureVal < sv[9] {
				if featureVal < sv[8] {
					pos = 8
				} else {
					pos = 9
				}
			} else if featureVal < sv[10] {
				pos = 10
			} else {
				pos = 11
			}
		} else if featureVal < sv[13] {
			if featureVal < sv[12] {
				pos = 12
			} else {
				pos = 13
			}
		} else if featureVal < sv[14] {
			pos = 14
		} else {
			pos = 15
		}
	}

	// Insert and shift.
	if pos >= 0 {
		for i := 15; i > pos; i-- {
			sv[i] = sv[i-1]
			age[i] = age[i-1]
		}
		sv[pos] = featureVal
		age[pos] = 1
	}

	// Get current median.
	var currentMedian int16 = 1600
	if v.frameCounter > 2 {
		currentMedian = sv[2]
	} else if v.frameCounter > 0 {
		currentMedian = sv[0]
	}

	// Smooth the median.
	var alpha int16
	if v.frameCounter > 0 {
		if currentMedian < v.meanVal[ch] {
			alpha = smoothDown
		} else {
			alpha = smoothUp
		}
	}
	tmp32 := int32(alpha+1)*int32(v.meanVal[ch]) +
		int32(32767-alpha)*int32(currentMedian) + 16384
	v.meanVal[ch] = int16(tmp32 >> 15)

	return v.meanVal[ch]
}
