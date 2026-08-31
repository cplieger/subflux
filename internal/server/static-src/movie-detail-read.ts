// The movie detail's on-demand payload, read in ONE place.
//
// Two callers need it and their FAILURE POLICIES are opposites: a navigation
// paints what answered (a failed read is an empty list, the page stays
// usable), while the transaction page leg refuses the commit (E3 step 3) so a
// half-answer cannot be committed as truth. That difference is the caller's,
// not the read's — so this returns both the raw per-read results (for the
// policy that inspects status) and the collapsed values (for the policy that
// paints), and neither caller spells the endpoints, the state-ids query, or
// the empty-list fallback again.
import { coverageMovieSubsRaw, stateIDsRaw } from "./wire/client.gen.js";
import type { ApiResult } from "./api-client.js";
import type { SubtitleEntry } from "./wire/types.gen.js";

/** What renderMovieDetail paints from: the movie's subtitle rows, and the ids
 *  that decide whether it gets a History button. */
export interface MovieDetailReads {
  readonly subs: SubtitleEntry[];
  readonly historyIDs: string[];
}

export interface MovieDetailRead {
  /** Per-read results, for a caller that judges failure (a 404 is a
   *  definitive answer — the item vanished — every other non-2xx is not). */
  readonly subs: ApiResult<SubtitleEntry[]>;
  readonly historyIDs: ApiResult<string[]>;
  /** Both reads collapsed to renderable values: a read that did not answer
   *  reads empty, independently of its sibling. */
  readonly reads: MovieDetailReads;
}

/** Read the pair. Store-only reads on both sides, so neither carries
 *  `?recovery=1` — there is no arr cache behind them to wave. */
export async function readMovieDetail(
  tmdbId: string | number,
  opts?: { readonly signal?: AbortSignal },
): Promise<MovieDetailRead> {
  const signal = opts?.signal;
  const [subs, historyIDs] = await Promise.all([
    coverageMovieSubsRaw(tmdbId, signal ? { signal } : {}),
    stateIDsRaw({ type: "movie", prefix: `tmdb-${String(tmdbId)}` }, signal ? { signal } : {}),
  ]);
  warnUnanswered("subs", subs, signal);
  warnUnanswered("history ids", historyIDs, signal);
  return {
    subs,
    historyIDs,
    reads: { subs: subs.data ?? [], historyIDs: historyIDs.data ?? [] },
  };
}

/** The collapse above substitutes an empty list for an answer that never came,
 *  and the navigation caller has nowhere else to say so — an empty subs table
 *  is a legitimate paint, so without this a genuine failure is indistinguishable
 *  from a movie with no subtitles. 404 is excluded because it IS an answer (the
 *  item is gone), and an abort is the caller's own doing. */
function warnUnanswered(what: string, r: ApiResult<unknown>, signal?: AbortSignal): void {
  if (r.ok || r.status === 404 || signal?.aborted) {
    return;
  }
  console.warn("movie detail:", what, r.status, r.error);
}
