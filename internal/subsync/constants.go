package subsync

// MaxModel is the corpus gate's verdict: Linear, with the end-fit ground,
// refused fewer same-cut subtitles than piecewise and its drop state at
// zero delivered-and-wrong, so Align and the jobs never ask AlignReference
// past ModelLinear and only this package's tests reach the piecewise path.
const MaxModel = ModelLinear

// The reference pipeline's constants: one owner per number.
const (
	minSpans = 5
	maxSpans = 10_000
	// windowMs bounds every offset a sweep or the DP considers.
	windowMs = 600_000
	// linearMargin is how much better than the shift's rating a frame-rate
	// ratio must rate to be selected.
	linearMargin = 1.10
	gridMs       = 100
	refineMs     = 10
	// ratingTie is the relative gap below which two refinement ratings tie:
	// a float sum of n positive terms errs by under n×1.1e-16 of itself.
	ratingTie   = 1e-9
	heldOutGain = 0.10
	// splitPenaltyPer is decision 28's change cost, × min(n_ref, n_inc), in
	// rating units.
	splitPenaltyPer = 0.030
	// checkpointEvery: the DP keeps its main row and drop-state ring every
	// this many cues and recomputes each block at traceback.
	checkpointEvery = 1_024
	// The cut study's monoDP settings (configuration m3).
	dropEnter   = 1.0 // × the split penalty, entering a drop run
	dropLeave   = 1.0 // × the split penalty, leaving it
	lambda      = 1.0 // a kept cue pays lambda × its chance rating
	onsetWeight = 1.0 // the start-onset kernel: 1 at an exact start match
	onsetMs     = 800 // and 0 beyond this distance
	// maxRiseMs is the slack for alternate takes: between two kept cues the
	// mapping may lengthen the gap by at most this much.
	maxRiseMs = 3_000

	// MaxAudioFrames bounds the audio path's frames, 10 ms each: about 83
	// minutes, the FFT's memory at 500,000 frames.
	MaxAudioFrames   = 500_000
	audioAgreementMs = 500

	referenceGate = 0.6 // decision 28
	audioGate     = 0.5 // decision 27, on peak × 0.9

	// The coverage guard's thresholds, the corpus gate's loosest values at
	// zero delivered-and-wrong. edgeGate is under referenceGate: a fit
	// between the two is delivered at Identity, Unverified, while one whose
	// end runs fit worse than edgeGate is refused.
	maxEdgeSpeechMs = 23_760
	edgeCues        = 16
	edgeGate        = 0.49
)

// ratios are the linear model's candidate scales.
var ratios = [6]float64{25.0 / 24, 25 / 23.976, 24.0 / 25, 23.976 / 25, 1001.0 / 1000, 1000.0 / 1001}

// The audio path's two decider settings over one LLR stream reproduce the
// safe and precise passes; each alignment makes its own deciders.
const (
	safeThreshold    = 250
	safeOverhang     = 35
	preciseThreshold = 125
	preciseOverhang  = 10
)
