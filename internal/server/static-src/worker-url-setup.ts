// worker-url-setup.ts — cmd/bundle's page define, installed as a global for
// the suites (globals.d.ts declares it; events.worker.test.ts asserts the
// spawn against this value). Not a vitest `define`: Browser Mode installs a
// define's string verbatim, so a JSON-quoted value reaches the page with its
// quotes and a bare path is refused as an invalid expression.
(globalThis as { __SSE_WORKER_URL__?: string }).__SSE_WORKER_URL__ = "/chunks/sse-worker-test.js";
