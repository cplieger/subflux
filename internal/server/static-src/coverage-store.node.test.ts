// The coverage layering, read off the source: coverage-store.ts is the LEAF
// under both coverage orchestrators, and neither the A6 reset rule nor the
// heal's detail coupling travels on the event bus any more.
//
// A node test because the subject is the import graph, which is a filesystem
// read; nothing here renders. Its value is that the alternative — a direct
// import between the two orchestrators, or a bus hop reintroduced to dodge the
// cycle that import would create — compiles and passes every other test.
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";

function src(name: string): string {
  return readFileSync(new URL(name, import.meta.url), "utf8");
}

/** The `./x.js` specifiers one module imports, resolved back to `.ts` names. */
function imports(module: string): string[] {
  const found = new Set<string>();
  for (const m of src(module).matchAll(/from "\.\/([^"]+)\.js"/g)) {
    const name = m[1];
    if (name !== undefined) {
      found.add(`${name}.ts`);
    }
  }
  return [...found].sort();
}

/** Every module reachable from `entry` through local imports. Generated wire
 *  modules are leaves by construction and are not walked. */
function reachableFrom(entry: string): Set<string> {
  const seen = new Set<string>();
  const queue = [entry];
  while (queue.length > 0) {
    const next = queue.pop();
    if (next === undefined || seen.has(next) || next.startsWith("wire/")) {
      continue;
    }
    seen.add(next);
    queue.push(...imports(next));
  }
  seen.delete(entry);
  return seen;
}

describe("coverage layering", () => {
  it("nothing the store leaf reaches is an orchestrator", () => {
    // Transitive, not direct: a leaf that reaches coverage.ts through a third
    // module is the same cycle, and it is the one a direct-import check misses.
    const reachable = [...reachableFrom("coverage-store.ts")].sort();

    expect(reachable).not.toContain("coverage.ts");
    expect(reachable).not.toContain("coverage-heal.ts");
    expect(reachable).not.toContain("page-leg.ts");
  });

  it("the heal writes rows through the store and never through the pair writer", () => {
    expect(imports("coverage-heal.ts")).toContain("coverage-store.ts");
    expect(reachableFrom("coverage-heal.ts")).not.toContain("coverage.ts");
  });

  it("the pair writer sits above the heal, so the reset rule is a direct call", () => {
    const covImports = imports("coverage.ts");

    expect(covImports).toContain("coverage-heal.ts");
    expect(covImports).toContain("coverage-store.ts");
    expect(src("coverage.ts")).toContain("resetCoverageHeal()");
  });

  it("neither invariant travels on the bus", () => {
    const bus = src("bus.ts");

    // A synchronous invariant routed through a notification bus is what the
    // leaf exists to make unnecessary; the events are gone, not merely unused.
    expect(bus).not.toContain("coverage:overwrite");
    expect(bus).not.toContain("refresh:series-detail");
    expect(imports("coverage-heal.ts")).not.toContain("bus.ts");
  });
});
