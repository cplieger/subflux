// Vitest 4.1 configuration for subflux TypeScript unit tests.
// Default environment: node (pure functions, no DOM overhead).
// DOM modules: add `// @vitest-environment happy-dom` at the top of the
// test file to get window/document/localStorage/etc. No browser binary
// needed — happy-dom is a pure JS DOM implementation running in Node.
// Run: vitest --run (single pass) or vitest (watch mode)
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Default: node. Override per test file with:
    //   // @vitest-environment happy-dom
    environment: "node",
    pool: "threads",

    // Each test file gets its own module graph. isolate:false was faster but
    // unsound for this suite: several files replace a WHOLE module for their
    // own purposes (events.test.ts stubs ./status.js, status.test.ts mocks
    // @cplieger/actions to capture every action registration), and with a
    // shared registry whichever file loads first wins for the rest of the
    // worker. status.test.ts then saw a status.js whose top-level apiAction
    // calls had already run under events.test.ts's mock, so its dispatcher map
    // stayed empty and the stop-control test failed with "undefined is not a
    // spy". It only surfaced where workers are scarce enough to pack those two
    // files together, which is why it was invisible locally and red in CI on
    // every run: 0 failures in 5 local attempts at 20 CPUs, 1 in 3 pinned to 4,
    // 100% on the 4-CPU runner. vibekit's config carries the same note after
    // the same bug. Measured cost here: 1.48s to 1.53s.
    isolate: true,

    include: ["**/*.test.ts"],
    // .stryker-tmp holds Stryker's sandbox, a full copy of this directory. A
    // run that dies before cleanTempDir leaves it behind, and without this the
    // next plain `vitest --run` collects every test twice.
    exclude: ["../static/**", "node_modules/**", "**/.stryker-tmp/**"],

    passWithNoTests: false,
    allowOnly: false,
    globals: false,

    expect: {
      requireAssertions: true,
    },

    clearMocks: true,
    mockReset: true,
    restoreMocks: true,
    unstubEnvs: true,
    unstubGlobals: true,

    bail: process.env["CI"] ? 1 : 0,

    testTimeout: 2000,
    hookTimeout: 5000,
    slowTestThreshold: 100,

    sequence: {
      shuffle: { files: false, tests: false },
      concurrent: false,
      hooks: "stack",
    },

    // Loaded once per worker before any test file. Configures fast-check
    // global defaults (numRuns, verbosity, time limits). See file for
    // tuning rationale.
    setupFiles: ["./fc-strict-setup.ts"],

    printConsoleTrace: true,
    expandSnapshotDiff: true,

    // TypeScript type-checking is handled by tsc --noEmit (via validate-local.sh
    // and CI). Vitest's built-in typecheck is experimental and redundant here.

    coverage: {
      provider: "v8",
      // Report all TS source files, not just those imported by tests.
      include: ["*.ts"],
      exclude: [
        "*.test.ts",
        "*.d.ts",
        // No genuinely untestable modules in subflux static-src —
        // all DOM modules are testable with happy-dom environment.
        // Add exclusions here only if a module requires canvas/WebGL
        // or a real browser context.
      ],
      reportOnFailure: true,
      reporter: ["text", "text-summary", "lcov"],
      thresholds: {
        lines: 60,
        functions: 60,
        branches: 50,
        statements: 60,
        perFile: true,
      },
    },

    chaiConfig: {
      truncateThreshold: 0,
      showDiff: true,
      includeStack: true,
    },

    experimental: {
      fsModuleCache: true,
      fsModuleCachePath: ".vitest-cache",
    },
  },
});
