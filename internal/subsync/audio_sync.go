package subsync

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
	"github.com/cplieger/subflux/internal/subsync/vad"
	"golang.org/x/sync/errgroup"
)

// AudioSyncHints provides content characteristics for adaptive strategy
// selection. Zero values use the default strategy.
type AudioSyncHints struct {
	// DialogueCues and MaskCues are pre-classified ASS cues from the native
	// parser. When set, audioSyncFromPCM uses these directly instead of
	// applying text-based heuristics (karaoke pair detection, SDH filtering)
	// to the flat cue list. This produces better results for ASS files
	// because style-based classification is more accurate than text patterns.
	DialogueCues []Cue
	MaskCues     []Cue

	// DurationSec is the total media duration in seconds.
	DurationSec int

	// IsASS indicates the subtitle was ASS-extracted (clean cues, no tag remnants).
	IsASS bool

	// DisableVAD skips the GMM VAD fallback (Strategy D). Used for benchmarking
	// to measure the energy-only pipeline's accuracy.
	DisableVAD bool
}

// Audio sync offset limits. The correlation can produce arbitrarily large
// audioSyncConfig consolidates all audio sync tuning parameters for
// inspectability and testability.
type audioSyncConfig struct {
	MaxOffsetPct int64          // percentage of audio duration
	MaxOffsetMs  int64          // hard ceiling in milliseconds
	MinOffsetMs  int64          // hard floor for short clips
	VADSafe      audioVADConfig // recall-biased pass
	VADPrecise   audioVADConfig // precision-biased pass
	AgreementMs  int64          // max disagreement between passes
}

var defaultAudioSyncConfig = audioSyncConfig{
	MaxOffsetPct: 10,
	MaxOffsetMs:  300_000, // 5 minutes
	MinOffsetMs:  30_000,  // 30 seconds
	VADSafe:      audioVADConfig{mode: vad.ModeVeryAggressive, threshold: 250, overhangFrames: 35, adaptScale: 0},
	VADPrecise:   audioVADConfig{mode: vad.ModeVeryAggressive, threshold: 125, overhangFrames: 10, adaptScale: 0},
	AgreementMs:  500,
}

// audioVADConfig is one pass of GMM-VAD tuning. Each pass runs
// vad.FramesBinaryThresholdTuned with these parameters; the safe and
// precise passes use complementary tunings (safe pass biases toward
// recall; precise pass biases toward precision).
type audioVADConfig struct {
	mode           vad.Mode // VAD aggressiveness (vad.ModeQuality to vad.ModeVeryAggressive)
	threshold      float64  // overhang threshold in milliseconds
	overhangFrames int      // overhang frames percentage
	adaptScale     float64  // 0 = frozen adaptation (best for movie audio)
}

// audioSync performs audio-based synchronization without a reference subtitle.
//
// Adaptive strategy selection based on content characteristics:
//   - Coverage < 0.3 (very sparse): 10-frame smoothing for faster response
//   - ASS format or duration > 90 min: no-trim mask (more signal available)
//   - Default: trimmed mask (best for TV/SRT)
//
// All paths use voice-band (300-3000 Hz) energy with SDH-filtered mask
// (excludes [sound effects], music notes, parenthesized descriptions).
// Broadband energy+flux runs as a complementary strategy. Onset correlation
// provides mask-bias fallback.
func audioSync(ctx context.Context, incorrect []Cue, videoPath string, hints AudioSyncHints) SyncResult {
	if len(incorrect) < MinCuesForSync {
		slog.Debug("audio sync: skipped, too few cues", "cues", len(incorrect), "min", MinCuesForSync)
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}

	pcm, err := ffmpeg.ExtractSegmentPCM(ctx, videoPath, 0, 0)
	if err != nil {
		slog.Warn("audio sync: extraction failed", "path", videoPath, "error", err)
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}

	return audioSyncFromPCM(ctx, incorrect, pcm, hints)
}

// audioSyncFromPCM performs audio-based synchronization on pre-extracted PCM.
// Uses a 2-step GMM classification with frozen adaptation:
//   - Safe pass (t250-oh35): high pass rate, catches difficult content
//   - Precise pass (t125-oh10): tighter residuals for easy content
//
// If both agree within 500ms, uses the precise result.
func audioSyncFromPCM(ctx context.Context, incorrect []Cue, pcm []int16, hints AudioSyncHints) SyncResult {
	if len(incorrect) < MinCuesForSync {
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}

	samplesPerFrame := ffmpeg.PCMSampleRate * frameMs / 1000
	numFrames := len(pcm) / samplesPerFrame
	if numFrames == 0 {
		slog.Debug("audio sync: insufficient PCM samples for one frame",
			"pcm_samples", len(pcm),
			"samples_per_frame", samplesPerFrame)
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}
	audioDurMs := int64(numFrames) * frameMs

	// Cue classification: use pre-classified ASS dialogue cues when
	// available. The ASS parser classifies styles as dialogue or
	// non-dialogue; filterDialogueCues handles text-level filtering
	// (karaoke pair removal, SDH).
	analysisCues := classifyDialogue(hints.DialogueCues, incorrect)

	if len(analysisCues) == 0 {
		slog.Debug("audio sync: no dialogue cues after filtering",
			"original_cues", len(incorrect),
			"hints_dialogue", len(hints.DialogueCues))
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}

	// Build bipolar subtitle signal from cleaned cues.
	vadSubSignal := buildVADSubSignal(analysisCues, numFrames)
	if len(vadSubSignal) == 0 {
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}

	// 2-step GMM: run once, threshold at two levels.
	// Frozen adaptation (adaptScale=0) works better on movie audio.
	var safeSig, preciseSig []float64
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		safeSig = runAudioVADPass(gctx, pcm, defaultAudioSyncConfig.VADSafe)
		return gctx.Err()
	})
	g.Go(func() error {
		preciseSig = runAudioVADPass(gctx, pcm, defaultAudioSyncConfig.VADPrecise)
		return gctx.Err()
	})
	if err := g.Wait(); err != nil {
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}

	safeLen := min(len(safeSig), len(vadSubSignal))
	precLen := min(len(preciseSig), len(vadSubSignal))

	safeCorr := crossCorrelateEdges(ctx, safeSig[:safeLen], vadSubSignal[:safeLen])
	if err := ctx.Err(); err != nil {
		return SyncResult{Cues: incorrect, Confidence: ConfidenceNone, Method: MethodAudio}
	}
	precCorr := crossCorrelateEdges(ctx, preciseSig[:precLen], vadSubSignal[:precLen])

	corr := safeCorr
	signalType := "gmm-safe"

	if abs64(safeCorr.OffsetMs-precCorr.OffsetMs) <= defaultAudioSyncConfig.AgreementMs {
		corr = precCorr
		signalType = "gmm-precise"
	} else {
		slog.Debug("audio sync: safe/precise disagree, using safe",
			"safe_offset_ms", safeCorr.OffsetMs,
			"prec_offset_ms", precCorr.OffsetMs,
			"diff_ms", abs64(safeCorr.OffsetMs-precCorr.OffsetMs))
	}

	slog.Info("audio sync complete",
		"offset_ms", corr.OffsetMs,
		"peak", corr.Peak,
		"signal", signalType,
		"is_ass", hints.IsASS,
		"dialogue_cues", len(analysisCues),
		"safe_offset", safeCorr.OffsetMs,
		"safe_peak", safeCorr.Peak,
		"prec_offset", precCorr.OffsetMs,
		"prec_peak", precCorr.Peak,
		"audio_dur_s", audioDurMs/1000)

	conf := Confidence(corr.Peak * float64(DefaultConfidenceCaps.ForMethod(MethodAudio)))
	if corr.OffsetMs == 0 {
		return SyncResult{
			Cues: incorrect, Offset: 0, Rate: 1.0,
			Confidence: conf, Method: MethodAudio,
		}
	}

	// Reject offsets that are unreasonably large. The threshold is the
	// smaller of the percentage-based limit and the hard ceiling, but
	// never below the hard floor (protects short clips from false rejects).
	absOff := abs64(corr.OffsetMs)
	pctLimit := max(audioDurMs*defaultAudioSyncConfig.MaxOffsetPct/100, defaultAudioSyncConfig.MinOffsetMs)
	limit := min(pctLimit, defaultAudioSyncConfig.MaxOffsetMs)
	if audioDurMs > 0 && absOff > limit {
		slog.Info("audio sync: discarding excessive offset",
			"offset_ms", corr.OffsetMs,
			"audio_dur_ms", audioDurMs,
			"limit_ms", limit)
		return SyncResult{
			Cues: incorrect, Offset: 0, Rate: 1.0,
			Confidence: ConfidenceNone, Method: MethodAudio,
		}
	}

	offset := time.Duration(corr.OffsetMs) * time.Millisecond
	return SyncResult{
		Cues: ShiftCues(incorrect, offset), Offset: corr.OffsetMs,
		Rate: 1.0, Confidence: conf, Method: MethodAudio,
	}
}

// classifyDialogue returns the cues that should drive VAD correlation.
// If ASS-preclassified dialogue cues are provided, they're used as the
// source; otherwise the full incoming cue list is used. In both cases,
// karaoke pairs are detected and filterDialogueCues strips SDH and
// karaoke-pair noise before returning.
func classifyDialogue(hintCues, fallbackCues []Cue) []Cue {
	src := fallbackCues
	if len(hintCues) > 0 {
		src = hintCues
	}
	karaokePairs := markKaraokePairs(src)
	return filterDialogueCues(src, karaokePairs)
}

// buildVADSubSignal creates a bipolar {-1, +1} subtitle presence signal
// for VAD correlation. Frames with active dialogue cues are +1, others -1.
func buildVADSubSignal(cues []Cue, numFrames int) []float64 {
	if numFrames == 0 {
		return nil
	}
	sig := make([]float64, numFrames)
	for i := range sig {
		sig[i] = -1.0
	}

	// Sort cues by start time for single-pass fill.
	sorted := make([]Cue, len(cues))
	copy(sorted, cues)
	slices.SortFunc(sorted, func(a, b Cue) int {
		return int(a.Start - b.Start)
	})

	// Single-pass: track the rightmost filled frame to skip overlaps.
	filled := 0
	for _, c := range sorted {
		sf := max(int(c.Start.Milliseconds()/frameMs), 0)
		ef := min(int(c.End.Milliseconds()/frameMs), numFrames)
		if sf < filled {
			sf = filled
		}
		for f := sf; f < ef; f++ {
			sig[f] = 1.0
		}
		if ef > filled {
			filled = ef
		}
	}
	return sig
}

// runAudioVADPass runs one GMM-VAD pass with the given config.
// Centralizes the call to vad.FramesBinaryThresholdTuned so that
// defaultAudioSyncConfig.VADSafe / defaultAudioSyncConfig.VADPrecise tuning lives as named values rather
// than positional magic numbers in audio_sync.
func runAudioVADPass(ctx context.Context, pcm []int16, c audioVADConfig) []float64 {
	return vad.FramesBinaryThresholdTuned(ctx, pcm, c.mode, c.threshold, c.overhangFrames, c.adaptScale, vad.Tuning{})
}
