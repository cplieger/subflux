package subsync

// MaxModel is the corpus gate's verdict on the piecewise stage and its drop
// state: they ship off, so Align and the jobs never ask AlignReference past
// ModelLinear. Only this package's tests reach the piecewise path, until a
// corpus-gate run records zero delivered-and-wrong with it on.
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
	heldOutGain  = 0.10
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

	// The coverage guard's defaults; the corpus gate fixes each and reports
	// the false refusals it costs.
	maxGapSpeechMs  = 30_000
	maxEdgeSpeechMs = 0
	// maxExtraLinesMs passes one advertising line and refuses a 20 s stretch,
	// which reads 7.5 s at the least in the synthetic suite: the run omits
	// the cues within maxRiseMs of dialogue at both of its ends.
	maxExtraLinesMs = 5_000
	// The end-fit ground's defaults; the corpus gate fixes both.
	edgeCues = 16
	edgeGate = referenceGate
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
