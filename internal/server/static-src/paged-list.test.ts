// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { createPagedList } from "./paged-list.js";

describe("paged-list", () => {
  let container: HTMLElement;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
  });

  it.todo("reload fetches first page and renders items");

  it.todo("reload shows empty message when no items returned");

  it.todo("reload shows error div on fetch failure");

  it.todo("loadMore appends next page items to existing");

  it.todo("loadMore preserves scroll position");

  it.todo("Show More button appears when hasMore is true");

  it.todo("Show More button absent when hasMore is false");

  it.todo("concurrent reload calls are debounced (loading guard)");

  it.todo("concurrent loadMore calls are debounced (loading guard)");
});
