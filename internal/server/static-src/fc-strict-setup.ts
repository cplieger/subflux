// Strict global defaults for fast-check property-based tests.
//
// Loaded via vitest's `setupFiles` so every property test inherits the
// same bar without per-file boilerplate. Settings prioritize bug
// detection over speed; CI runs sequentially and these tests are
// pure-function focused, so the wall-time cost is acceptable.
//
// Tuning:
// - numRuns: 1000 (10x fast-check's default 100). Buys deeper coverage
//   of the input space at ~10x runtime per property; for typical
//   per-property runtime <100ms this still keeps each property under
//   1s on modern hardware.
// - verbose: VeryVerbose. When a property fails, fast-check prints the
//   full shrunken counter-example plus the path it took. Necessary for
//   debugging; harmless on success (only prints on failure).
// - interruptAfterTimeLimit: 10s safety bound. Without it, an
//   accidentally pathological generator could hang vitest indefinitely.
// - markInterruptAsFailure: true. Treats an interrupted property as a
//   failed test, surfacing it in CI rather than silently passing.
// - endOnFailure: false. Always shrink to the minimal counter-example;
//   the few extra ms after the first failure are worth it for the
//   debugging payoff.
// - No fixed seed: randomness across runs is the value. CI flakes from
//   newly-discovered counter-examples are bug reports, not test
//   infrastructure failures.

import fc from "fast-check";

fc.configureGlobal({
  numRuns: 1000,
  verbose: fc.VerbosityLevel.VeryVerbose,
  endOnFailure: false,
  interruptAfterTimeLimit: 10000,
  markInterruptAsFailure: true,
});
