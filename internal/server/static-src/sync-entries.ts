// sync-entries.ts — what the sync dialog derives from its `entries` input.
//
// Both functions are pure: they take values and return values, touch no module
// state and no DOM. They live here rather than in sync.ts because that is what
// makes them testable at all — sync.ts reads ambient module state, so nothing
// inside it can be arranged by a test. Same treatment, same reason, as
// sync-timecode.ts, which was extracted from this dialog earlier.

import { langName } from "./utils.js";
import { DEFAULT_VARIANT } from "./constants.js";
import type { SubtitleEntry } from "./api-types.js";

export interface LabeledEntry {
  sub: SubtitleEntry;
  label: string;
}

/** buildSyncSubLabels creates display labels for the subtitle dropdown.
 *
 *  Groups by language+variant and numbers duplicates only when they exist, so a
 *  lone English track reads "English" rather than "English #1":
 *  "English", "English #1", "English #2", "English SDH", "French".
 *
 *  A missing language renders as "??" rather than being dropped: the entry is
 *  still selectable, because the user can see a track the server sent and an
 *  absent label would make it unreachable. */
export function buildSyncSubLabels(entries: readonly SubtitleEntry[]): LabeledEntry[] {
  // Count how many entries share the same language+variant.
  const counts: Record<string, number> = {};
  for (const sub of entries) {
    const key = `${sub.language || ""}|${sub.variant || DEFAULT_VARIANT}`;
    counts[key] = (counts[key] ?? 0) + 1;
  }

  // Assign labels with numbering only when duplicates exist.
  const seen: Record<string, number> = {};
  const labels: LabeledEntry[] = [];
  for (const sub of entries) {
    const lang = langName(sub.language || "??");
    const v = sub.variant || DEFAULT_VARIANT;
    const key = `${sub.language || ""}|${v}`;
    const total = counts[key] ?? 0;

    let label = lang;
    if (v === "hi") {
      label += " SDH";
    } else if (v === "forced") {
      label += " Forced";
    }

    if (total > 1) {
      seen[key] = (seen[key] ?? 0) + 1;
      label += ` #${seen[key]}`;
    }

    labels.push({ sub, label });
  }
  return labels;
}

/** parseSeasonEpisode reads the aired season/episode off a series media_id
 *  (`tvdb-12345-s01e05`), for the video MediaRef the preview needs.
 *
 *  A movie media_id has no `s##e##` suffix and yields 0/0, which the server
 *  ignores — so the zero is a real answer here, not a parse failure, and the
 *  caller does not branch on it. */
export function parseSeasonEpisode(mediaID: string): { season: number; episode: number } {
  const m = /s(\d+)e(\d+)$/.exec(mediaID);
  if (!m) {
    return { season: 0, episode: 0 };
  }
  return { season: Number(m[1]), episode: Number(m[2]) };
}
