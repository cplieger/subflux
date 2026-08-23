// wizard-example.test.ts — Pins EXAMPLE_SECTIONS against the YAML authority
// (config.example.yaml at the repo root, the file main.go embeds and fresh
// boots auto-write). EXAMPLE_SECTIONS hand-duplicates that file for the
// wizard's differs-from-example collapse gate; if the two drift, the gate
// misjudges which steps are placeholder data. The expectation here is read
// FROM THE FILE at test time and parsed by a test-local YAML-subset parser —
// never derived from the production constant — so production and tests
// cannot drift in lockstep.
//
// No YAML dependency exists in package.json (the yaml/js-yaml packages in
// node_modules are transitive deps of knip/vite/stylelint — phantom imports),
// so the parser below covers exactly the example file's constructs: nested
// maps, block sequences of scalars and maps, flow sequences, quoted strings,
// booleans, integers, and quote-aware comments. Anything else throws, so a
// future construct outside the subset fails loudly instead of mis-parsing.

import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { EXAMPLE_SECTIONS } from "./wizard-example.js";

// static-src -> server -> internal -> repo root.
const EXAMPLE_YAML_URL = new URL("../../../config.example.yaml", import.meta.url);

// --- Minimal YAML-subset parser (test-local) --------------------------------

interface Line {
  indent: number;
  text: string;
}

/** stripComment removes a `#` comment (quote-aware: a # inside a double-
 *  quoted scalar is content; a comment # must sit at line start or after a
 *  space, per YAML). */
function stripComment(raw: string): string {
  let inStr = false;
  for (let i = 0; i < raw.length; i++) {
    const c = raw[i];
    if (c === '"') {
      inStr = !inStr;
    } else if (c === "#" && !inStr && (i === 0 || raw[i - 1] === " ")) {
      return raw.slice(0, i);
    }
  }
  return raw;
}

function toLines(src: string): Line[] {
  const out: Line[] = [];
  for (const raw of src.split("\n")) {
    const stripped = stripComment(raw).trimEnd();
    if (stripped.trim() === "") {
      continue;
    }
    out.push({
      indent: stripped.length - stripped.trimStart().length,
      text: stripped.trim(),
    });
  }
  return out;
}

function parseScalar(s: string): unknown {
  if (s === "true") {
    return true;
  }
  if (s === "false") {
    return false;
  }
  if (/^-?\d+$/.test(s) || /^-?\d+\.\d+$/.test(s)) {
    return Number(s);
  }
  if (s.startsWith('"') && s.endsWith('"') && s.length >= 2) {
    return s.slice(1, -1);
  }
  return s;
}

/** parseInline handles a scalar or a flow sequence ([], [a, b]). */
function parseInline(s: string): unknown {
  if (s.startsWith("[") && s.endsWith("]")) {
    const inner = s.slice(1, -1).trim();
    if (inner === "") {
      return [];
    }
    return inner.split(",").map((p) => parseScalar(p.trim()));
  }
  return parseScalar(s);
}

const KEY_RE = /^([a-z_][a-z0-9_]*):(.*)$/;

interface Cursor {
  i: number;
}

function parseNode(lines: Line[], pos: Cursor, indent: number): unknown {
  const line = lines[pos.i];
  if (!line) {
    throw new Error("unexpected end of document");
  }
  return line.text.startsWith("- ") ? parseSeq(lines, pos, indent) : parseMap(lines, pos, indent);
}

function parseMap(lines: Line[], pos: Cursor, indent: number): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  while (pos.i < lines.length) {
    const line = lines[pos.i]!;
    if (line.indent !== indent || line.text.startsWith("- ")) {
      break;
    }
    const m = KEY_RE.exec(line.text);
    if (!m) {
      throw new Error(`unsupported map line: ${line.text}`);
    }
    const key = m[1]!;
    const rest = m[2]!.trim();
    pos.i++;
    if (rest !== "") {
      out[key] = parseInline(rest);
      continue;
    }
    const next = lines[pos.i];
    if (!next || next.indent <= indent) {
      throw new Error(`key "${key}" has no value (construct outside the parser's subset)`);
    }
    out[key] = parseNode(lines, pos, next.indent);
  }
  return out;
}

function parseSeq(lines: Line[], pos: Cursor, indent: number): unknown[] {
  const out: unknown[] = [];
  while (pos.i < lines.length) {
    const line = lines[pos.i]!;
    if (line.indent !== indent || !line.text.startsWith("- ")) {
      break;
    }
    const rest = line.text.slice(2).trim();
    if (KEY_RE.test(rest)) {
      // Map item: treat the inline pair as the map's first key at the item
      // body indent (marker indent + 2 in this file) and parse the map —
      // continuation keys of the same item sit at that indent.
      lines[pos.i] = { indent: indent + 2, text: rest };
      out.push(parseMap(lines, pos, indent + 2));
    } else {
      pos.i++;
      out.push(parseInline(rest));
    }
  }
  return out;
}

function parseYamlSubset(src: string): Record<string, unknown> {
  const lines = toLines(src);
  const pos: Cursor = { i: 0 };
  const doc = parseMap(lines, pos, 0);
  if (pos.i !== lines.length) {
    throw new Error(`unparsed content: ${lines[pos.i]?.text ?? "<eof>"}`);
  }
  return doc;
}

// --- Parity ------------------------------------------------------------------

/** The config sections owned by wizard steps (wizard-state.ts
 *  STEP_SECTIONS, flattened and hardcoded here on purpose): exactly these
 *  feed the differs-from-example gate, so exactly these must agree with the
 *  YAML authority. */
const WIZARD_SECTIONS = [
  "sonarr",
  "radarr",
  "media_roots",
  "providers",
  "languages",
  "search",
  "adaptive",
  "scoring",
  "post_processing",
] as const;

function loadAuthority(): Record<string, unknown> {
  return parseYamlSubset(readFileSync(EXAMPLE_YAML_URL, "utf8"));
}

describe("EXAMPLE_SECTIONS parity with config.example.yaml", () => {
  it("parser self-check: non-wizard sections read back with hardcoded values", () => {
    const doc = loadAuthority();
    // These sections are NOT in EXAMPLE_SECTIONS, so they verify the parser
    // (and the file) independently of the constant under test.
    expect(doc["poll_interval"]).toBe("30s");
    expect(doc["trusted_proxies"]).toEqual([]);
    expect(doc["logging"]).toEqual({ level: "info", format: "json" });
    expect(doc["embedded_subtitles"]).toEqual({
      ignore_pgs: true,
      ignore_vobsub: true,
      ignore_ass: false,
    });
  });

  it("pins trap-critical authority values straight from the YAML (parser + file shape)", () => {
    const doc = loadAuthority();
    const sonarr = doc["sonarr"] as Record<string, unknown>;
    expect(sonarr["url"]).toBe("http://sonarr:8989");
    expect(sonarr["api_key"]).toBe("");
    expect(sonarr["enabled"]).toBe(true);
    expect(doc["media_roots"]).toEqual(["/media"]);
    const languages = doc["languages"] as Record<string, unknown>;
    expect((languages["rules"] as unknown[]).length).toBe(4);
    expect((languages["rules"] as unknown[])[0]).toEqual({
      audio: "en",
      subtitles: [{ code: "fr", min_score: 80 }],
    });
    expect(languages["default"]).toEqual([{ code: "en" }, { code: "fr" }]);
  });

  it("every wizard-owned section equals the YAML authority exactly", () => {
    const doc = loadAuthority();
    for (const key of WIZARD_SECTIONS) {
      expect(EXAMPLE_SECTIONS[key], `section "${key}"`).toEqual(doc[key]);
    }
  });

  it("EXAMPLE_SECTIONS carries exactly the wizard-owned sections, nothing else", () => {
    expect(Object.keys(EXAMPLE_SECTIONS).sort()).toEqual([...WIZARD_SECTIONS].sort());
  });
});
