// @vitest-environment node
// Lint test: catches regressions to the action framework policy.
//
// Policy: user-initiated mutations must go through the actions
// framework (defineAction / apiAction). Background reads, validation
// probes, and best-effort cleanup stay silent.
//
// What this test asserts:
//   - No new write-shaped calls outside the explicit allowlist:
//       * `void apiPost(`, `void apiDelete(`, `void apiPut(`,
//         `void apiPatch(` (fire-and-forget mutations)
//       * `await apiPostRaw(`, `await apiDeleteRaw(`, `await apiPutRaw(`,
//         `await apiPatchRaw(` (callers bypassing the framework's
//         lifecycle wrapper to read raw ApiResult)
//       * `await apiPost(`, `await apiDelete(`, `await apiPut(`,
//         `await apiPatch(` (callers bypassing the framework's
//         null-on-failure wrapper)
//   - GET reads (`apiGet` / `apiGetRaw` / `apiGetTyped` / `apiGetTypedRaw`
//     / `apiPostTyped`) are NOT lint-checked because they're read-only
//     or used for typed-decoder validation patterns.
//   - The allowlist consists of:
//       * actions/*.ts (the framework + per-area action defs)
//       * api-client.ts (the underlying transport)
//       * test files (*.test.ts)
//       * specific files with documented exceptions (BACKGROUND_ALLOWLIST)
//
// If you're adding a new user-initiated mutation, declare an action in
// actions/<area>.ts (or inline in the calling module) and dispatch it.
// See actions/index.ts and the steering doc for guidance.
//
// If you're adding a legitimate validation probe, fire-and-forget
// cleanup, or auth flow that needs raw response access, add the file
// to BACKGROUND_ALLOWLIST below with a one-line comment explaining why.
// ---------------------------------------------------------------------------

import { describe, it, expect } from "vitest";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const ROOT = join(import.meta.dirname, "..");

/** Files where a write-shaped call is permitted because they're
 *  documented exceptions to the framework policy.
 *
 *  NOTE: Matching is by basename only (the last path segment). This
 *  means entries must be unique filenames across the source tree. If
 *  two files share a basename and only one should be allowlisted,
 *  switch to relative-path matching for that entry. */
const BACKGROUND_ALLOWLIST = new Set<string>([
  // Auth flows that need raw response access:
  //   - login.ts: rate-limit error reads Retry-After header from the
  //     response; multi-step state machine (TOTP-required, account-
  //     disabled branches) is more readable as raw apiPostRaw + status
  //     checks than dispatch + onError + branching.
  //   - security.ts: 6 auth ops (reauth, password change, TOTP
  //     enable/confirm/disable, passkey delete, API-key create) all
  //     use inline form feedback (showFeedback) rather than toasts.
  //     Framework value-props don't apply.
  //   See subflux-actions.md "When NOT to migrate" for the full rationale.
  "login.ts",
  "security.ts",

  // Wizard validation probe: validateAsync runs during step input to
  // check whether a typed path exists on disk. It's a read disguised
  // as POST (server needs structured args). Inline error string return
  // is the right shape — toast wiring would be wrong here.
  "wizard-steps.ts",

  // Best-effort logout: redirects regardless of server response.
  // Action wrapping would add noise without changing behavior.
  "user-menu.ts",
]);

/** Regex for forbidden patterns. Each match is a regression candidate.
 *  Catches:
 *    - `void apiX(`  (fire-and-forget — preferred form for floating promises)
 *    - `await apiX(` (caller bypassing the framework's lifecycle wrapper)
 *    - `await apiXRaw(` (caller bypassing the framework to read raw ApiResult)
 *    - bare `apiX(` at start-of-line / start-of-statement (a floating promise
 *      with no `void` prefix — silently ignored TypeScript anti-pattern that
 *      would also bypass the framework). The negative-lookbehind in the regex
 *      excludes `import`/`return`/`from`/`=`/`.`/`(`/`,`/whitespace-only
 *      contexts so we only flag actual call invocations. */
const PATTERNS: { name: string; re: RegExp }[] = [
  { name: "void apiPost", re: /\bvoid\s+apiPost\s*[<(]/g },
  { name: "void apiDelete", re: /\bvoid\s+apiDelete\s*[<(]/g },
  { name: "void apiPut", re: /\bvoid\s+apiPut\s*[<(]/g },
  { name: "void apiPatch", re: /\bvoid\s+apiPatch\s*[<(]/g },
  { name: "await apiPost", re: /\bawait\s+apiPost\s*[<(]/g },
  { name: "await apiDelete", re: /\bawait\s+apiDelete\s*[<(]/g },
  { name: "await apiPut", re: /\bawait\s+apiPut\s*[<(]/g },
  { name: "await apiPatch", re: /\bawait\s+apiPatch\s*[<(]/g },
  { name: "await apiPostRaw", re: /\bawait\s+apiPostRaw\s*[<(]/g },
  { name: "await apiDeleteRaw", re: /\bawait\s+apiDeleteRaw\s*[<(]/g },
  { name: "await apiPutRaw", re: /\bawait\s+apiPutRaw\s*[<(]/g },
  { name: "await apiPatchRaw", re: /\bawait\s+apiPatchRaw\s*[<(]/g },
  // Bare invocation: `apiPost(...)` at start of statement (preceded only by
  // whitespace on the line). Catches floating-promise anti-pattern that
  // bypasses both `void` and `await`. Restricted to start-of-line so we
  // don't flag legitimate uses like `const r = apiPost(...)` (assignment),
  // `return apiPost(...)`, or `someFn(apiPost(...))` (argument).
  { name: "bare apiPost", re: /^\s*apiPost\s*[<(]/gm },
  { name: "bare apiDelete", re: /^\s*apiDelete\s*[<(]/gm },
  { name: "bare apiPut", re: /^\s*apiPut\s*[<(]/gm },
  { name: "bare apiPatch", re: /^\s*apiPatch\s*[<(]/gm },
  { name: "bare apiPostRaw", re: /^\s*apiPostRaw\s*[<(]/gm },
  { name: "bare apiDeleteRaw", re: /^\s*apiDeleteRaw\s*[<(]/gm },
  { name: "bare apiPutRaw", re: /^\s*apiPutRaw\s*[<(]/gm },
  { name: "bare apiPatchRaw", re: /^\s*apiPatchRaw\s*[<(]/gm },
];

function listTSFiles(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) {
      // Skip vendored / build dirs and the framework itself.
      if (name === "node_modules" || name === ".vitest-cache" || name === "actions" || name === "wire") continue;
      listTSFiles(p, out);
    } else if (name.endsWith(".ts") && !name.endsWith(".test.ts") && !name.endsWith(".d.ts")) {
      out.push(p);
    }
  }
  return out;
}

describe("action framework — regression guard", () => {
  it("no new write-shaped api calls outside the allowlist", () => {
    const violations: string[] = [];
    for (const file of listTSFiles(ROOT)) {
      const rel = relative(ROOT, file);
      const base = rel.split("/").pop() ?? rel;
      if (BACKGROUND_ALLOWLIST.has(base)) continue;
      // Skip api-client itself — it's the underlying primitive.
      if (base === "api-client.ts") continue;
      const src = readFileSync(file, "utf8");
      for (const { name, re } of PATTERNS) {
        for (const m of src.matchAll(re)) {
          const lineIdx = src.slice(0, m.index).split("\n").length;
          violations.push(`${rel}:${String(lineIdx)}: ${name} (${m[0].trim()})`);
        }
      }
    }
    if (violations.length > 0) {
      const message = [
        "User-initiated mutations must go through the actions framework.",
        "See actions/index.ts and the subflux-actions steering doc.",
        "If this is a legitimate validation probe / fire-and-forget cleanup /",
        "auth flow needing raw response access, add the file to",
        "BACKGROUND_ALLOWLIST in actions/lint.test.ts with a one-line comment.",
        "",
        "Violations:",
        ...violations.map((v) => `  ${v}`),
      ].join("\n");
      throw new Error(message);
    }
    expect(violations).toEqual([]);
  });
});
