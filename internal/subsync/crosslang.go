package subsync

import (
	"context"
	"unsafe"

	"github.com/cplieger/subflux/internal/subsync/crosslang"
)

// crossLangAlign delegates to the crosslang sub-package.
func crossLangAlign(ctx context.Context, reference, incorrect []Cue) SyncResult {
	// Zero-copy conversion: Cue and crosslang.Cue have identical layout.
	var _ [unsafe.Sizeof(Cue{})]byte = [unsafe.Sizeof(crosslang.Cue{})]byte{} //nolint:staticcheck // QF1011: explicit type is intentional compile-time size assertion

	refCL := unsafe.Slice((*crosslang.Cue)(unsafe.Pointer(unsafe.SliceData(reference))), len(reference))
	incCL := unsafe.Slice((*crosslang.Cue)(unsafe.Pointer(unsafe.SliceData(incorrect))), len(incorrect))

	result := crosslang.Align(ctx, refCL, incCL)

	var cues []Cue
	if len(result.Cues) > 0 {
		cues = unsafe.Slice((*Cue)(unsafe.Pointer(unsafe.SliceData(result.Cues))), len(result.Cues))
	}

	return SyncResult{
		Cues:       cues,
		Offset:     result.Offset,
		Rate:       result.Rate,
		Confidence: Confidence(result.Confidence),
		Method:     MethodCrosslang,
	}
}
