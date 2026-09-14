// subjects.ts — the digest subjects this tab holds, and the Subject-Stamp
// header that feeds them.
//
// The server stamps every subject-bearing GET with `{kind, ref, version,
// epoch}` (internal/server/stamp.go), read before the handler runs so the
// version is never ahead of the body it travels with. The transport
// (api-client.ts) observes the stamp of every successful GET into ONE version
// map, so a subject enters the map the moment a loader lands it, whichever
// flavor of the generated client the loader used; events.ts hands the same
// map to the stream, whose digest asks the server which held versions moved.
//
// A LEAF: only the library's types, so the transport, the legs and the
// stream can all reach it without a cycle.

import { createVersionMap, type Subject, type VersionMap } from "@cplieger/sse";

export const SUBJECT_STAMP_HEADER = "Subject-Stamp";

/** The subject kinds the server mints (internal/server/events/versions.go). */
export const SUBJECT_JOBS: Subject = { kind: "jobs", ref: "" };
export const SUBJECT_HISTORY: Subject = { kind: "history", ref: "" };

/** The `detail` subject of one series or movie root (`tvdb-N` / `tmdb-N`). */
export function detailSubject(root: string): Subject {
  return { kind: "detail", ref: root };
}

export interface Stamp {
  readonly kind: string;
  readonly ref: string;
  readonly version: string;
  readonly epoch: string;
}

const EPOCH_RE = /^[0-9a-f]{16}$/;
const VERSION_RE = /^(0|[1-9][0-9]*)$/;

/** The stamp a response carries, or null when the header is absent or is not
 *  a well-formed stamp. Read as unknown: a malformed stamp observes nothing,
 *  so the subject reads `changed` at the next digest, the safe direction. */
export function stampOf(headers: Headers | undefined): Stamp | null {
  const raw = headers?.get(SUBJECT_STAMP_HEADER);
  if (raw === null || raw === undefined) {
    return null;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null) {
    return null;
  }
  const rec = parsed as Record<string, unknown>;
  const kind = rec["kind"];
  const ref = rec["ref"];
  const version = rec["version"];
  const epoch = rec["epoch"];
  if (typeof kind !== "string" || kind === "" || typeof ref !== "string") {
    return null;
  }
  if (typeof version !== "string" || !VERSION_RE.test(version)) {
    return null;
  }
  if (typeof epoch !== "string" || !EPOCH_RE.test(epoch)) {
    return null;
  }
  return { kind, ref, version, epoch };
}

let versions: VersionMap = createVersionMap();

/** The one version map: the stream binds it on every hello, the transport
 *  fills it, the digest reads it. */
export function versionMap(): VersionMap {
  return versions;
}

/** Record a landed response's stamp, if it carries one. Called by the
 *  transport after a successful GET, which is the commit point for every
 *  loader in this app: a GET that did not land observes nothing. */
export function observeStamp(headers: Headers | undefined): void {
  const stamp = stampOf(headers);
  if (stamp === null) {
    return;
  }
  versions.observe({ kind: stamp.kind, ref: stamp.ref }, stamp.version, stamp.epoch);
}

/** Drop a subject this tab no longer renders, so the digest never names a
 *  leg the tab cannot run. */
export function forgetSubject(subject: Subject): void {
  versions.forget(subject);
}

/** Test-only: a fresh, unbound map. */
export function _resetSubjectsForTest(): void {
  versions = createVersionMap();
}
