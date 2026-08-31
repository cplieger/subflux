// view-scope.test.ts — the disposal contract itself.
//
// Every registration in this app that outlives a DOM node — a bindList
// binding, a reactive effect, a registry entry — is released by this module, so
// the ordering and idempotence rules below are what every caller relies on. The
// per-caller wiring is pinned where it lives (detail-scan for the scan-button
// registry, detail/coverage/files/history for the render bindings, page-leg for
// the route leave).
import { describe, it, expect, beforeEach } from "vitest";
import {
  contentView,
  historyView,
  movieViewId,
  ownedByRoute,
  releaseRouteViews,
  seriesViewId,
} from "./view-scope.js";

beforeEach(() => {
  releaseRouteViews();
  contentView.clear();
  historyView.clear();
});

describe("view-scope: a scope's own registrations", () => {
  it("runs each disposer once, newest first", () => {
    const scope = contentView.mount("v");
    const order: string[] = [];
    scope.add(() => order.push("first"));
    scope.add(() => order.push("second"));

    scope.dispose();
    scope.dispose();

    expect(order).toEqual(["second", "first"]);
  });

  it("terminates when a disposer disposes its own scope", () => {
    // A disposer is free to release whatever it owns, which can reach back to
    // this scope; without the disposed latch that re-enters the disposer list.
    const scope = contentView.mount("v");
    let runs = 0;
    scope.add(() => {
      runs++;
      scope.dispose();
    });

    scope.dispose();

    expect(runs).toBe(1);
  });

  it("runs a disposer registered after disposal immediately", () => {
    // The subtree it would have belonged to is already gone, so deferring it
    // would leak it forever.
    const scope = contentView.mount("v");
    scope.dispose();
    let ran = false;

    scope.add(() => {
      ran = true;
    });

    expect(ran).toBe(true);
  });
});

describe("view-scope: child scopes", () => {
  it("disposes every open child with the parent", () => {
    const view = contentView.mount("v");
    const released: string[] = [];
    view.child("a").add(() => released.push("a"));
    view.child("b").add(() => released.push("b"));

    view.dispose();

    expect(released.sort()).toEqual(["a", "b"]);
  });

  it("disposes the previous child when the same key is re-opened", () => {
    // A row repaint discards what the last paint built, so re-opening the row's
    // scope must release what that paint registered.
    const view = contentView.mount("v");
    let released = 0;
    view.child("row").add(() => {
      released++;
    });

    view.child("row");

    expect(released).toBe(1);
  });

  it("releases one child without touching its siblings", () => {
    const view = contentView.mount("v");
    let gone = false;
    let kept = false;
    view.child("row-1").add(() => {
      gone = true;
    });
    view.child("row-2").add(() => {
      kept = true;
    });

    view.release("row-1");

    expect(gone).toBe(true);
    expect(kept).toBe(false);
  });

  it("hands back an already-disposed child from a disposed parent", () => {
    // A render that keeps building rows after its view was released must not
    // leave them registered anywhere.
    const view = contentView.mount("v");
    view.dispose();
    let ran = false;

    view.child("row").add(() => {
      ran = true;
    });

    expect(ran).toBe(true);
  });
});

describe("view-scope: a host holds one view", () => {
  it("releases the previous occupant when the next view mounts", () => {
    let released = false;
    contentView.mount(seriesViewId("1")).add(() => {
      released = true;
    });

    contentView.mount(movieViewId("2"));

    expect(released).toBe(true);
  });

  it("answers scopeFor only for the view that is the occupant", () => {
    const scope = contentView.mount(seriesViewId("1"));

    expect(contentView.scopeFor(seriesViewId("1"))).toBe(scope);
    expect(contentView.scopeFor(movieViewId("1"))).toBeNull();

    contentView.mount(movieViewId("1"));

    expect(contentView.scopeFor(seriesViewId("1"))).toBeNull();
  });

  it("answers scopeFor with nothing after clear, and releases the occupant", () => {
    let released = false;
    contentView.mount("v").add(() => {
      released = true;
    });

    contentView.clear();

    expect(released).toBe(true);
    expect(contentView.scopeFor("v")).toBeNull();
  });

  it("keeps the two hosts independent", () => {
    // The coverage pane and the history pane are separate containers; a view
    // mounting into one must not release the other's.
    let contentReleased = false;
    contentView.mount("library").add(() => {
      contentReleased = true;
    });

    historyView.mount("history");

    expect(contentReleased).toBe(false);
    expect(contentView.scopeFor("library")).not.toBeNull();
  });
});

describe("view-scope: views the router owns", () => {
  it("releases a route-owned view on the leave path", () => {
    let released = false;
    const view = contentView.mount(seriesViewId("1"));
    ownedByRoute(view);
    view.add(() => {
      released = true;
    });

    releaseRouteViews();

    expect(released).toBe(true);
  });

  it("leaves a view the route does not own alone", () => {
    // The library view is the pane's steady state and survives a route
    // re-apply; only the views a route mounted die with it.
    let released = false;
    contentView.mount("library").add(() => {
      released = true;
    });

    releaseRouteViews();

    expect(released).toBe(false);
    expect(contentView.scopeFor("library")).not.toBeNull();
  });

  it("releases a route-owned view registered after a handoff", () => {
    // Each detail view registers with the router as it mounts, so the
    // registration that follows a host handoff is the live view's — losing it
    // would leave that view outliving its route.
    const first = contentView.mount(seriesViewId("1"));
    ownedByRoute(first);
    const second = contentView.mount(seriesViewId("2"));
    ownedByRoute(second);
    let released = false;
    second.add(() => {
      released = true;
    });

    releaseRouteViews();

    expect(released).toBe(true);
  });
});
