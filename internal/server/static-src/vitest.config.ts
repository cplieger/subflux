// Vitest 4.1 configuration for subflux TypeScript unit tests.
//
// Two projects, and the DEFAULT is the browser. A test file runs in a real
// headless Chromium unless its name opts out, because the browser is the
// environment this UI actually ships into and a DOM emulator got several
// assertions in sibling repos wrong for free.
//
// The opt-out is the `.node.test.ts` suffix: the reason lives in the stem, the
// placement in the suffix, so membership is readable off the filename instead
// of enumerated here where the list would drift undetected. Exactly two files
// carry it, and both need a genuine filesystem read:
//
//   - theme-snippet.node.test.ts reads ../static/index.html and login.html to
//     compare the inlined anti-FOUC bytes against the library snippet.
//   - wizard-example.node.test.ts reads config.example.yaml from the repo
//     root, which sits above this Vite root.
//
// A test misplaced INTO the browser project throws on the `node:fs` import, so
// that direction is loud and self-correcting. Fuzz keeps its own axis:
// `*.fuzz.test.ts` is how ts-ci selects fuzz targets.
//
// The include globs are package-root-relative, not `src/**`, because the tests
// sit beside the modules they cover.
//
// `channel: "chromium"` opts into Chromium's newer headless mode, the real
// browser rather than the separate headless-shell build. CI installs it with
// `npx playwright install --with-deps chromium`; locally it is a one-time
// `npx --no-install playwright install chromium`.
//
// Run: vitest --run (single pass) or vitest (watch mode)
import { playwright } from "@vitest/browser-playwright";
import { defineConfig } from "vitest/config";

// Test files this package never collects, in either project. .stryker-tmp
// holds Stryker's sandbox, a full copy of this directory: a run that dies
// before cleanTempDir leaves it behind, and without this the next plain
// `vitest --run` collects every test twice.
const sharedExclude = ["../static/**", "node_modules/**", "**/.stryker-tmp/**"];

export default defineConfig({
  test: {
    // Everything from here down to `projects` is the SHARED block, and each
    // project opts into it with `extends: true`. A project inherits NOTHING
    // from this block by default: without that flag the suite silently loses
    // expect.requireAssertions, allowOnly, mockReset, clearMocks,
    // restoreMocks, sequence, unstubGlobals and both timeouts, and it stays
    // green while it does, because losing a strictness option never fails a
    // test. Verified rather than trusted: a throwaway zero-assertion test
    // FAILS under requireAssertions in both projects.
    exclude: sharedExclude,

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

    testTimeout: 5000,
    hookTimeout: 10000,
    // Root-only in vitest 4: a per-project slowTestThreshold is not read.
    slowTestThreshold: 300,

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

    chaiConfig: {
      truncateThreshold: 0,
      showDiff: true,
      includeStack: true,
    },

    projects: [
      {
        // `extends: true` MUST sit here, as a sibling of `test`. Written
        // inside the `test` block it is accepted and silently ignored, and
        // the project then inherits NOTHING -- measured on vitest 4.1.11:
        // setupFiles never load, testTimeout reverts to the default, and
        // expect.requireAssertions never fires, all while the suite stays
        // green, because losing a strictness option never fails a test.
        extends: true,
        test: {
          name: "node",
          environment: "node",
          include: ["**/*.node.test.ts"],
        },
      },
      {
        extends: true,
        test: {
          name: "browser",
          include: ["**/*.test.ts"],
          // Inherited options are REPLACED, not merged, by a project that
          // restates one, so this exclude has to carry the shared entries too.
          exclude: ["**/*.node.test.ts", ...sharedExclude],
          // Per-FILE isolation is Browser Mode's own guarantee, so dropping
          // the old `isolate: true` preserves the property rather than
          // regressing it. It has to hold: several files replace a WHOLE
          // module for their own purposes (events.test.ts stubs ./status.js,
          // status.test.ts mocks @cplieger/actions to capture every action
          // registration), and with a shared registry whichever file loads
          // first wins for the rest of the worker. status.test.ts then saw a
          // status.js whose top-level apiAction calls had already run under
          // events.test.ts's mock, so its dispatcher map stayed empty and the
          // stop-control test failed with "undefined is not a spy". Under the
          // old thread pool it only surfaced where workers are scarce enough
          // to pack those two files together: 0 failures in 5 local attempts
          // at 20 CPUs, 1 in 3 pinned to 4, 100% on the 4-CPU runner.
          browser: {
            enabled: true,
            headless: true,
            provider: playwright({
              launchOptions: {
                channel: "chromium",
              },
            }),
            instances: [{ browser: "chromium" }],
            // Fixed viewport so layout-dependent assertions are reproducible;
            // a real browser computes real boxes, unlike the emulator this
            // replaced.
            viewport: { width: 1280, height: 720 },
            // A failure screenshot per failing test is noise in CI and cannot
            // be read from a job log; the assertion diff is the artifact.
            screenshotFailures: false,
          },
        },
      },
    ],

    // TypeScript type-checking is handled by tsc --noEmit (via validate-local.sh
    // and CI). Vitest's built-in typecheck is experimental and redundant here.

    // Coverage is a whole-process option and merges across both projects, so
    // it belongs here rather than in either one.
    coverage: {
      provider: "v8",
      // Report every TS source file in this directory, imported by a test or
      // not. The pattern has no slash, which micromatch reads as `**/*.ts`, so
      // it reaches subdirectories too — `wire/` is excluded below rather than
      // by narrowing this, because a narrower glob here would also stop
      // reporting a future subdirectory of hand-written modules.
      include: ["*.ts"],
      exclude: [
        "*.test.ts",
        "*.d.ts",
        // Generated by `go run ./cmd/wire-codegen` from internal/wirespec, and
        // in the gate only because the include glob recurses. The decoders are
        // cplieger/wiregen's output, so their correctness is that library's
        // contract and its test suite; the generated shape is pinned on the Go
        // side by internal/server/wirespec_consistency_test.go. Same
        // classification knip.json already makes ("ignore": ["wire/*.gen.ts"]).
        "wire/*.gen.ts",
        // The two esbuild entrypoints (cmd/bundle/main.go EntryPoints, and
        // knip.json's "entry"). Both export nothing: every function is
        // module-private and the module runs its side effects at load, so there
        // is no unit to import. Driving them would be an integration test of a
        // whole page, which is not what this per-file gate measures.
        "app.ts",
        "login.ts",
        // No genuinely untestable modules in subflux static-src — every DOM
        // module is testable in the browser project. Add exclusions here only
        // if a module needs a capability headless Chromium lacks.
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

    experimental: {
      fsModuleCache: true,
      fsModuleCachePath: ".vitest-cache",
    },
  },
});
