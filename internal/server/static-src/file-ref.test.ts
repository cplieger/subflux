// Tests for refKey's keyenc encoding (dedupe + collection identity).
import { describe, it, expect } from "vitest";
import fc from "fast-check";
import { split } from "@cplieger/keyenc";
import { refKey, type FileRefArgs } from "./file-ref.js";

/** The pre-keyenc expression, kept here so the collision it allowed is pinned
 *  as a fact rather than described in a comment. */
function pipeJoinedRefKey(ref: FileRefArgs): string {
  return `${ref.media_type}|${ref.media_id}|${ref.language}|${ref.variant ?? "standard"}|${
    ref.source ?? "external"
  }|${ref.ordinal ?? 0}`;
}

/** What a naive move to the new separator would have produced — the shape
 *  keyenc has to beat, not just match. */
function colonJoinedRefKey(ref: FileRefArgs): string {
  return [
    ref.media_type,
    ref.media_id,
    ref.language,
    ref.variant ?? "standard",
    ref.source ?? "external",
    String(ref.ordinal ?? 0),
  ].join(":");
}

function ref(over: Partial<FileRefArgs> = {}): FileRefArgs {
  return {
    media_type: "episode",
    media_id: "tvdb-100-s01e02",
    language: "fr",
    variant: "standard",
    source: "external",
    ordinal: 0,
    ...over,
  };
}

describe("refKey", () => {
  it("emits ordinary fields verbatim: only the separator changed", () => {
    // Every field here is free of both reserved characters (`:` and `\`), so
    // keyenc emits each one untouched and the key is exactly the ':' join.
    expect(refKey(ref())).toBe("episode:tvdb-100-s01e02:fr:standard:external:0");
    expect(refKey(ref())).toBe(pipeJoinedRefKey(ref()).replaceAll("|", ":"));
  });

  it("keeps two refs distinct where the pipe-joined key collapsed them", () => {
    // `language` arrives verbatim from the operator's config.yaml, so it can
    // carry the separator and shift the field split one place to the right.
    const a = ref({ media_id: "tvdb-1", language: "fr|forced", variant: "standard" });
    const b = ref({ media_id: "tvdb-1", language: "fr", variant: "forced|standard" });

    expect(pipeJoinedRefKey(a)).toBe(pipeJoinedRefKey(b)); // the defect
    expect(refKey(a)).not.toBe(refKey(b)); // fixed
  });

  it("keeps two refs distinct where a plain ':' join would also collapse them", () => {
    // Proof this is an encoding change, not a separator swap: the same forgery
    // aimed at the NEW separator still fails, because keyenc escapes it inside
    // the component instead of trusting the field not to contain it.
    const a = ref({ media_id: "tvdb-1", language: "fr:forced", variant: "standard" });
    const b = ref({ media_id: "tvdb-1", language: "fr", variant: "forced:standard" });

    expect(colonJoinedRefKey(a)).toBe(colonJoinedRefKey(b)); // naive form collapses
    expect(refKey(a)).not.toBe(refKey(b)); // keyenc does not
  });

  it("round-trips every field through split, whatever the fields contain", () => {
    // split is join's exact inverse, so recovering the six fields byte-for-byte
    // proves the encoding is injective — no field content can forge another
    // field's boundary at any position.
    fc.assert(
      fc.property(
        fc.string({ maxLength: 40 }),
        fc.string({ maxLength: 40 }),
        fc.string({ maxLength: 40 }),
        fc.string({ maxLength: 40 }),
        fc.nat(),
        (mediaID, language, variant, source, ordinal) => {
          const r = ref({ media_id: mediaID, language, variant, source, ordinal });
          expect(split(refKey(r))).toEqual([
            "episode",
            mediaID,
            language,
            variant,
            source,
            String(ordinal),
          ]);
        },
      ),
    );
  });
});
