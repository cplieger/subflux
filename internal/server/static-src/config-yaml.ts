// config-yaml.ts — Pure YAML parsing/extraction helpers for config.ts.
// No DOM, store, or side-effect dependencies; unit-testable in node env.

/**
 * Split raw YAML text into top-level keyed sections.
 * Each section starts at a line matching /^[a-z_]+:/ (not indented, not a comment).
 */
export function parseYAMLSections(lines: string[]): Record<string, string> {
  const sections: Record<string, string> = {};
  let current: string | null = null;
  let buf: string[] = [];
  for (const line of lines) {
    if (/^[a-z_]+:/.exec(line) && !line.startsWith(" ") && !line.startsWith("#")) {
      if (current) {
        sections[current] = buf.join("\n");
      }
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- split always has [0]
      current = line.split(":")[0]!;
      buf = [line];
    } else if (current) {
      buf.push(line);
    }
  }
  if (current) {
    sections[current] = buf.join("\n");
  }
  return sections;
}

/**
 * Extract a scalar value for `key` from a raw YAML block.
 * Handles quoted values and inline comments.
 */
export function extractYAMLValue(raw: string, key: string): string {
  for (const line of raw.split("\n")) {
    const trimmed = line.trim();
    if (trimmed.startsWith(`${key}:`)) {
      let val = trimmed.substring(key.length + 1).trim();
      // Pure comment (no value before the #).
      if (val.startsWith("#")) {
        return "";
      }
      // Strip surrounding quotes first.
      if (
        (val.startsWith('"') && val.includes('"', 1)) ||
        (val.startsWith("'") && val.includes("'", 1))
      ) {
        // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- string is non-empty
        const quote = val[0]!;
        const endIdx = val.indexOf(quote, 1);
        val = val.substring(1, endIdx);
      } else {
        // Strip inline comments (everything after ` #`).
        const commentIdx = val.indexOf(" #");
        if (commentIdx >= 0) {
          val = val.substring(0, commentIdx).trim();
        }
        val = val.replace(/^["']|["']$/g, "");
      }
      return val;
    }
  }
  return "";
}

/**
 * Extract a nested block for `key` from a raw YAML block.
 * Returns the indented content below the key line.
 */
export function extractYAMLBlock(raw: string, key: string): string {
  const lines = raw.split("\n");
  let found = false;
  let indent = 0;
  const buf: string[] = [];
  for (const line of lines) {
    if (!found) {
      const trimmed = line.trim();
      if (trimmed.startsWith(`${key}:`)) {
        found = true;
        const rest = trimmed.substring(key.length + 1).trim();
        if (rest && rest !== "{}") {
          return rest;
        }
        indent = line.search(/\S/) + 2;
      }
    } else if (line.trim() === "" || line.search(/\S/) >= indent) {
      buf.push(line);
    } else {
      break;
    }
  }
  return buf.length > 0 ? buf.join("\n") : "";
}

/**
 * Parse the providers: block into per-provider raw text blocks.
 */
export function parseProviderBlocks(raw: string): Record<string, string> {
  const lines = raw.split("\n");
  const providers: Record<string, string> = {};
  let current: string | null = null;
  let buf: string[] = [];
  for (const line of lines) {
    if (line.startsWith("providers:")) {
      continue;
    }
    const match = /^ {2}([a-z_]+):/.exec(line);
    if (match) {
      if (current) {
        providers[current] = buf.join("\n");
      }
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- regex group [1] guaranteed by match
      current = match[1]!;
      buf = [line];
    } else if (current && (line.startsWith("    ") || line.trim() === "")) {
      buf.push(line);
    } else if (current) {
      providers[current] = buf.join("\n");
      current = null;
      buf = [];
    }
  }
  if (current) {
    providers[current] = buf.join("\n");
  }
  return providers;
}

/**
 * Format a Go duration (nanoseconds) into a human-readable string.
 */
export function formatDurationCfg(ns: number | string): string {
  if (!ns) {
    return "";
  }
  // Go durations come as nanoseconds in JSON.
  if (typeof ns === "number") {
    const secs = Math.floor(ns / 1e9);
    if (secs === 0) {
      return "";
    }
    if (secs < 60) {
      return `${secs}s`;
    }
    const mins = Math.floor(secs / 60);
    if (mins < 60) {
      return `${mins}m`;
    }
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) {
      return `${hrs}h`;
    }
    return `${Math.floor(hrs / 24)}D`;
  }
  return ns;
}
