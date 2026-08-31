// movie-detail-read.test.ts — the one movie-detail read.
//
// Two callers with opposite failure policies share this leaf (a navigation
// paints what answered, the transaction leg refuses the commit), so what is
// pinned here is the seam between them: both raw results survive for the caller
// that judges status, the collapse is per-read for the caller that paints, and
// the swallow is announced exactly when it hides something.
import { describe, it, expect, beforeEach, vi } from "vitest";
import { readMovieDetail } from "./movie-detail-read.js";
import type { ApiResult } from "./api-client.js";
import type { SubtitleEntry } from "./wire/types.gen.js";

const wire = vi.hoisted(() => ({
  subs: { ok: true, status: 200, data: [] } as ApiResult<unknown>,
  ids: { ok: true, status: 200, data: [] } as ApiResult<unknown>,
  calls: [] as { name: string; args: unknown[] }[],
}));
vi.mock("./wire/client.gen.js", () => ({
  coverageMovieSubsRaw: (...args: unknown[]) => {
    wire.calls.push({ name: "coverageMovieSubsRaw", args });
    return Promise.resolve(wire.subs);
  },
  stateIDsRaw: (...args: unknown[]) => {
    wire.calls.push({ name: "stateIDsRaw", args });
    return Promise.resolve(wire.ids);
  },
}));

const ROW: SubtitleEntry = {
  language: "en",
  variant: "",
  source: "external",
  codec: "srt",
  score: 90,
} as SubtitleEntry;

function ok<T>(data: T): ApiResult<T> {
  return { ok: true, status: 200, data };
}

function fail(status: number, error: string): ApiResult<never> {
  return { ok: false, status, error };
}

beforeEach(() => {
  wire.calls = [];
  wire.subs = ok([]);
  wire.ids = ok([]);
});

describe("readMovieDetail: the requests", () => {
  it("reads both endpoints under the movie's own id and query", async () => {
    await readMovieDetail(7);

    expect(wire.calls.map((c) => c.name)).toEqual(["coverageMovieSubsRaw", "stateIDsRaw"]);
    expect(wire.calls[0]?.args[0]).toBe(7);
    // Scoped to THIS movie: an unscoped history query would answer for another.
    expect(wire.calls[1]?.args[0]).toEqual({ type: "movie", prefix: "tmdb-7" });
  });

  it("threads one signal into both reads, and omits the option when there is none", async () => {
    const ac = new AbortController();
    await readMovieDetail(8, { signal: ac.signal });

    expect(wire.calls[0]?.args[1]).toEqual({ signal: ac.signal });
    expect(wire.calls[1]?.args[1]).toEqual({ signal: ac.signal });

    wire.calls = [];
    await readMovieDetail(9);

    expect(wire.calls[0]?.args[1]).toEqual({});
    expect(wire.calls[1]?.args[1]).toEqual({});
  });

  it("accepts a string id, since the router's captures arrive as text", async () => {
    await readMovieDetail("11");

    expect(wire.calls[0]?.args[0]).toBe("11");
    expect(wire.calls[1]?.args[0]).toEqual({ type: "movie", prefix: "tmdb-11" });
  });
});

describe("readMovieDetail: the two views of one answer", () => {
  it("hands back both raw results so a caller can judge failure", async () => {
    wire.subs = fail(503, "unavailable");
    wire.ids = ok(["tmdb-7"]);

    const r = await readMovieDetail(7);

    expect(r.subs.ok).toBe(false);
    expect(r.subs.status).toBe(503);
    expect(r.historyIDs.ok).toBe(true);
  });

  it("collapses each read independently, so one failure does not blank the other", async () => {
    wire.subs = ok([ROW]);
    wire.ids = fail(500, "boom");

    const r = await readMovieDetail(7);

    expect(r.reads.subs).toEqual([ROW]);
    expect(r.reads.historyIDs).toEqual([]);
  });

  it("reads an answered-but-empty body as empty, not as a failure", async () => {
    wire.subs = ok(null);

    const r = await readMovieDetail(7);

    expect(r.subs.ok).toBe(true);
    expect(r.reads.subs).toEqual([]);
  });
});

describe("readMovieDetail: announcing the swallow", () => {
  it("warns for a read that did not answer, naming which one", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    wire.subs = fail(503, "unavailable");

    await readMovieDetail(7);

    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn.mock.calls[0]).toEqual(["movie detail:", "subs", 503, "unavailable"]);
  });

  it("stays silent for a 404, which is an answer: the movie is gone", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    wire.subs = fail(404, "not found");
    wire.ids = fail(404, "not found");

    await readMovieDetail(7);

    expect(warn).not.toHaveBeenCalled();
  });

  it("stays silent when the caller aborted, which is not a failure to report", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    const ac = new AbortController();
    ac.abort();
    // The transport reports an aborted request as status 0.
    wire.subs = fail(0, "aborted");
    wire.ids = fail(0, "aborted");

    await readMovieDetail(7, { signal: ac.signal });

    expect(warn).not.toHaveBeenCalled();
  });
});
