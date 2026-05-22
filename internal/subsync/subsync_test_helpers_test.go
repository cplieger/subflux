package subsync

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"subflux/internal/api"
)

// syncReader synchronizes subtitle content from readers. It is the
// streaming variant of SyncFile for tests that want to avoid writing
// temp files to disk.
//
// Test-only: production flows all read from the filesystem via
// SyncFile. If an upload-sync flow is ever added, this body can move
// back into subsync.go and be exported as SyncReader.
func syncReader(ctx context.Context, videoPath string, subtitle, reference io.Reader, opts *Options) (Result, error) {
	if opts == nil {
		opts = &Options{}
	}

	subData, err := io.ReadAll(io.LimitReader(subtitle, api.MaxSafeFileBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("read subtitle: %w", err)
	}
	if int64(len(subData)) > api.MaxSafeFileBytes {
		return Result{}, fmt.Errorf("subtitle too large: %d bytes (max %d)", len(subData), api.MaxSafeFileBytes)
	}
	subData = NormalizeEncoding(subData)

	incCues, err := ParseSRT(bytes.NewReader(subData))
	if err != nil {
		return Result{}, fmt.Errorf("parse subtitle: %w", err)
	}
	if len(incCues) == 0 {
		return Result{Data: subData, Method: MethodNone}, nil
	}

	var refCues []Cue
	if reference != nil {
		refData, refErr := io.ReadAll(io.LimitReader(reference, api.MaxSafeFileBytes+1))
		if refErr != nil {
			return Result{}, fmt.Errorf("read reference: %w", refErr)
		}
		if int64(len(refData)) > api.MaxSafeFileBytes {
			return Result{}, fmt.Errorf("reference too large: %d bytes (max %d)", len(refData), api.MaxSafeFileBytes)
		}
		refData = NormalizeEncoding(refData)
		refCues, err = ParseSRT(bytes.NewReader(refData))
		if err != nil {
			return Result{}, fmt.Errorf("parse reference: %w", err)
		}
	}

	return syncAndBuild(ctx, videoPath, opts, refCues, incCues, "")
}

// allPostProcess returns PostProcessOptions with every step enabled. Used
// across test files that need a fully-configured post-processing pass.
func allPostProcess() PostProcessOptions {
	return PostProcessOptions{
		StripHI:              true,
		StripTags:            true,
		NormalizeEncoding:    true,
		NormalizeLineEndings: true,
		CleanWhitespace:      true,
		RemoveEmpty:          true,
	}
}
