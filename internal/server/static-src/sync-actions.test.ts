// Tests for the sync actions' dedupe keys and the audio dispatch's retry
// posture (site: sync-actions.ts).
//
// The dedupe key is not readable from an Action (the framework exposes only
// name/dispatch/cancel), so these drive the real framework and count the
// requests that reach the injected fetch: a collapsed key means the second
// dispatch never runs at all.
import { describe, it, expect, beforeEach } from "vitest";
import { configureApi } from "@cplieger/actions";
import { resetActionFramework } from "@cplieger/actions/testing";
import { audioSyncAction, saveManualOffsetAction, seasonSyncAction } from "./sync-actions.js";
import type { SyncAudioRequest, SyncOffsetRequest } from "./wire/types.gen.js";

const AUDIO_ACCEPTED = { activity_id: "act-1", job_id: 7 };

let requests: string[] = [];
let respondWith: () => Response = () =>
  new Response(JSON.stringify(AUDIO_ACCEPTED), {
    status: 202,
    headers: { "content-type": "application/json" },
  });

/** Records every request and answers the configured response. Dispatches are
 *  issued back-to-back synchronously, so the in-flight dedupe slot of the first
 *  is always visible to the second regardless of how fast this resolves. */
const recordingFetch: typeof fetch = (_input, init) => {
  requests.push(String(init?.body ?? ""));
  return Promise.resolve(respondWith());
};

function audioArgs(over: Partial<SyncAudioRequest> = {}): SyncAudioRequest {
  return {
    media_type: "episode",
    media_id: "tvdb-1-s01e01",
    language: "fr",
    variant: "standard",
    source: "external",
    ordinal: 0,
    ...over,
  };
}

function offsetArgs(over: Partial<SyncOffsetRequest> = {}): SyncOffsetRequest {
  return { ...audioArgs(), offset_ms: 500, ...over };
}

/** The original key: a raw template around the pipe-joined refKey. Kept so the
 *  collision it allowed is pinned as a fact rather than described in a
 *  comment. */
function originalAudioKey(a: SyncAudioRequest): string {
  return `sync.audio:${a.media_type}|${a.media_id}|${a.language}|${a.variant ?? "standard"}|${
    a.source ?? "external"
  }|${a.ordinal ?? 0}`;
}

describe("sync action dedupe keys", () => {
  beforeEach(() => {
    resetActionFramework(); // clears the in-flight dedupe map between tests
    configureApi({ baseUrl: "http://localhost", fetchFn: recordingFetch });
    requests = [];
    respondWith = () =>
      new Response(JSON.stringify(AUDIO_ACCEPTED), {
        status: 202,
        headers: { "content-type": "application/json" },
      });
  });

  it("collapses two concurrent dispatches of the SAME ref onto one request", () => {
    // The guard for the tests below: dedupe still works, so a second click on
    // one row does not run the analysis twice.
    const args = audioArgs();
    const first = audioSyncAction.dispatch(args);
    const second = audioSyncAction.dispatch(args);

    expect(requests.length).toBe(1);
    return Promise.all([first, second]);
  });

  it("runs both dispatches where the original dedupe key collapsed two refs", () => {
    // `language` comes verbatim from the operator's config.yaml, so it could
    // carry the separator and shift the pipe-joined refKey's field split. Both
    // dispatches then shared one key and the second silently never ran — no
    // sync for that subtitle, no error either.
    const a = audioArgs({ language: "fr|forced", variant: "standard" });
    const b = audioArgs({ language: "fr", variant: "forced|standard" });
    expect(originalAudioKey(a)).toBe(originalAudioKey(b)); // the defect

    const first = audioSyncAction.dispatch(a);
    const second = audioSyncAction.dispatch(b);

    expect(requests.length).toBe(2);
    return Promise.all([first, second]);
  });

  it("keeps the offset component distinct under the nested outer key", () => {
    // saveManualOffsetAction nests refKey as ONE component and appends the
    // offset as another, so two offsets for the same file stay two dispatches.
    const first = saveManualOffsetAction.dispatch(offsetArgs({ offset_ms: 500 }));
    const second = saveManualOffsetAction.dispatch(offsetArgs({ offset_ms: -500 }));

    expect(requests.length).toBe(2);
    return Promise.all([first, second]);
  });

  it("runs both offset saves where the original key collapsed two refs", () => {
    const a = offsetArgs({ language: "fr|forced", variant: "standard" });
    const b = offsetArgs({ language: "fr", variant: "forced|standard" });

    const first = saveManualOffsetAction.dispatch(a);
    const second = saveManualOffsetAction.dispatch(b);

    expect(requests.length).toBe(2);
    return Promise.all([first, second]);
  });
});

describe("the audio dispatch's retry posture (D3)", () => {
  beforeEach(() => {
    resetActionFramework();
    configureApi({ baseUrl: "http://localhost", fetchFn: recordingFetch });
    requests = [];
  });

  it("resolves the 202's ids", async () => {
    const outcome = await audioSyncAction.dispatch(audioArgs()).outcome;
    expect(outcome.status).toBe("success");
    if (outcome.status === "success") {
      expect(outcome.value).toEqual(AUDIO_ACCEPTED);
    }
  });

  it("the capacity 429 costs exactly ONE request and surfaces its status", async () => {
    // The framework's network classifier treats 429 as retryable; this
    // action carries NO retry, so the refusal must reach the dialog on the
    // first receipt — visible, never auto-retried.
    respondWith = () =>
      new Response(JSON.stringify({ error: "sync queue is full", code: "rate_limited" }), {
        status: 429,
        headers: { "content-type": "application/json" },
      });
    const outcome = await audioSyncAction.dispatch(audioArgs()).outcome;
    expect(requests.length).toBe(1);
    expect(outcome.status).toBe("error");
    if (outcome.status === "error") {
      expect((outcome.error as { status?: number }).status).toBe(429);
    }
  });

  it("a transient 5xx is not retried either — no timeout-retryable classification remains", async () => {
    respondWith = () =>
      new Response(JSON.stringify({ error: "boom" }), {
        status: 503,
        headers: { "content-type": "application/json" },
      });
    const outcome = await audioSyncAction.dispatch(audioArgs()).outcome;
    expect(requests.length).toBe(1);
    expect(outcome.status).toBe("error");
  });
});

describe("the season dispatch's posture (D2/D3)", () => {
  const SEASON_ACCEPTED = { activity_id: "act-9" };

  beforeEach(() => {
    resetActionFramework();
    configureApi({ baseUrl: "http://localhost", fetchFn: recordingFetch });
    requests = [];
    respondWith = () =>
      new Response(JSON.stringify(SEASON_ACCEPTED), {
        status: 202,
        headers: { "content-type": "application/json" },
      });
  });

  it("resolves the 202's batch activity id", async () => {
    const outcome = await seasonSyncAction.dispatch({ series_id: 42, season: 1 }).outcome;
    expect(outcome.status).toBe("success");
    if (outcome.status === "success") {
      expect(outcome.value).toEqual(SEASON_ACCEPTED);
    }
  });

  it("collapses a double-click of the same scope onto one request", () => {
    const first = seasonSyncAction.dispatch({ series_id: 42, season: 1 });
    const second = seasonSyncAction.dispatch({ series_id: 42, season: 1 });
    expect(requests.length).toBe(1);
    // A DIFFERENT season is a different key: it dispatches.
    const other = seasonSyncAction.dispatch({ series_id: 42, season: 2 });
    expect(requests.length).toBe(2);
    return Promise.all([first, second, other]);
  });

  it("the capacity 429 costs exactly ONE request — visible, never auto-retried", async () => {
    respondWith = () =>
      new Response(JSON.stringify({ error: "sync queue is full", code: "rate_limited" }), {
        status: 429,
        headers: { "content-type": "application/json" },
      });
    const outcome = await seasonSyncAction.dispatch({ series_id: 42, season: 1 }).outcome;
    expect(requests.length).toBe(1);
    expect(outcome.status).toBe("error");
    if (outcome.status === "error") {
      expect((outcome.error as { status?: number }).status).toBe(429);
    }
  });
});
