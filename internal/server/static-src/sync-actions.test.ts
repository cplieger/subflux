// Tests for the sync actions' dedupe keys (site: sync-actions.ts).
//
// The dedupe key is not readable from an Action (the framework exposes only
// name/dispatch/cancel), so these drive the real framework and count the
// requests that reach the injected fetch: a collapsed key means the second
// dispatch never runs at all.
import { describe, it, expect, beforeEach } from "vitest";
import { configureApi } from "@cplieger/actions";
import { resetActionFramework } from "@cplieger/actions/testing";
import { audioSyncAction, saveManualOffsetAction } from "./sync-actions.js";
import type { SyncAudioRequest, SyncOffsetRequest } from "./wire/types.gen.js";

const AUDIO_OK = { method: "audio", offset_ms: 250, confidence: 0.9, applied: true };

let requests: string[] = [];

/** Records every request and answers a valid decodable body. Dispatches are
 *  issued back-to-back synchronously, so the in-flight dedupe slot of the first
 *  is always visible to the second regardless of how fast this resolves. */
const recordingFetch: typeof fetch = (_input, init) => {
  requests.push(String(init?.body ?? ""));
  return Promise.resolve(
    new Response(JSON.stringify(AUDIO_OK), {
      status: 200,
      headers: { "content-type": "application/json" },
    }),
  );
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
