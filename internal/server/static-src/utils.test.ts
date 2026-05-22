// Unit tests for utils.ts — pure functions only.
// DOM-dependent functions (clickableRow, emptyState, langSelect) are excluded.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { prettyLabel, langName, fmtEpisode, tvdbMediaId, debounce } from "./utils.js";

// pad is not exported; tested indirectly via fmtEpisode / tvdbMediaId.

describe("prettyLabel", () => {
  const cases = [
    { name: "replaces underscores with spaces", input: "foo_bar", expected: "Foo Bar" },
    { name: "capitalises first letter of each word", input: "hello_world", expected: "Hello World" },
    { name: "handles single word", input: "hello", expected: "Hello" },
    { name: "handles already-capitalised input", input: "Hello_World", expected: "Hello World" },
    { name: "handles empty string", input: "", expected: "" },
    { name: "handles multiple underscores", input: "a_b_c", expected: "A B C" },
    { name: "capitalises after hyphens too", input: "foo-bar", expected: "Foo-Bar" },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(prettyLabel(tc.input)).toBe(tc.expected);
    });
  }
});

describe("langName", () => {
  const cases = [
    { name: "returns display name for known code (en)", input: "en", expected: "English" },
    { name: "returns display name for known code (fr)", input: "fr", expected: "French" },
    { name: "returns display name for known code (de)", input: "de", expected: "German" },
    { name: "returns uppercase code for unknown code (xx)", input: "xx", expected: "XX" },
    { name: "returns uppercase code for unknown code (zz)", input: "zz", expected: "ZZ" },
    { name: "passes through 'default' sentinel unchanged", input: "default", expected: "default" },
    { name: "passes through 'no targets' sentinel unchanged", input: "no targets", expected: "no targets" },
    { name: "returns empty string for empty input", input: "", expected: "" },
    { name: "handles Portuguese Brazil code", input: "pb", expected: "Portuguese (Brazil)" },
    { name: "handles Japanese", input: "ja", expected: "Japanese" },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(langName(tc.input)).toBe(tc.expected);
    });
  }
});

describe("fmtEpisode", () => {
  const cases = [
    { name: "formats single-digit season and episode with padding", season: 1, episode: 5, expected: "S01E05" },
    { name: "formats double-digit season and episode without extra padding", season: 12, episode: 34, expected: "S12E34" },
    { name: "formats season 0 (specials)", season: 0, episode: 1, expected: "S00E01" },
    { name: "formats episode 0", season: 1, episode: 0, expected: "S01E00" },
    { name: "formats large season/episode numbers", season: 100, episode: 200, expected: "S100E200" },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(fmtEpisode(tc.season, tc.episode)).toBe(tc.expected);
    });
  }
});

describe("tvdbMediaId", () => {
  it("builds composite ID with padded season and episode", () => {
    expect(tvdbMediaId(12345, 1, 5)).toBe("tvdb-12345-s01e05");
  });

  it("handles double-digit season and episode", () => {
    expect(tvdbMediaId(99, 12, 34)).toBe("tvdb-99-s12e34");
  });

  it("handles season 0", () => {
    expect(tvdbMediaId(1, 0, 1)).toBe("tvdb-1-s00e01");
  });

  it("uses the tvdb ID verbatim", () => {
    expect(tvdbMediaId(999999, 2, 3)).toBe("tvdb-999999-s02e03");
  });
});

describe("debounce", () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it("delays execution until after the wait period", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced();
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("resets timer on subsequent calls", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced();
    vi.advanceTimersByTime(50);
    debounced();
    vi.advanceTimersByTime(50);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(50);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("passes arguments from the last call", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced('a');
    debounced('b');
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledWith('b');
  });

  it("fires independently after each wait period", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 50);
    debounced();
    vi.advanceTimersByTime(50);
    debounced();
    vi.advanceTimersByTime(50);
    expect(fn).toHaveBeenCalledTimes(2);
  });
});
