// paged-list.ts — shared paginated list with server-side or client-side fetch.
//
// Both the history and library pages use this module. The caller provides:
// - a fetchPage callback that returns a page of items (server or in-memory)
// - a renderItems callback that builds DOM from the fetched items
// - a page size
//
// The module manages: accumulating items across pages, "show more" button,
// reset on filter change, and loading/error states.

import { el, patch, emptyDiv, errDiv } from './dom.js';

/** A single page of results from the data source. */
export interface Page<T> {
  /** Items in this page. */
  items: T[];
  /** Whether more items exist beyond this page. */
  hasMore: boolean;
}

/** Callback that fetches a page of items given offset and limit. */
type FetchPage<T> = (offset: number, limit: number) => Promise<Page<T>>;

/** Callback that renders accumulated items into a DocumentFragment. */
type RenderItems<T> = (items: T[]) => DocumentFragment;

/** Configuration for a paged list instance. */
export interface PagedListConfig<T> {
  /** DOM element to render into. */
  container: HTMLElement;
  /** Fetches a page of items. */
  fetchPage: FetchPage<T>;
  /** Renders the full accumulated item list into a fragment. */
  renderItems: RenderItems<T>;
  /** Items per page. */
  pageSize: number;
  /** Message shown when no items match. */
  emptyMessage: string;
  /** Message shown when no items exist at all (no filters active). */
  emptyNoData?: string;
}

/** Controls for a paged list instance. */
export interface PagedList {
  /** Reset and fetch the first page. Call when filters change. */
  reload: () => Promise<void>;
  /** Fetch the next page and append. Call from "show more". */
  loadMore: () => Promise<void>;
}

/**
 * Create a paged list that fetches and renders items incrementally.
 * Returns controls for reload (filter change) and loadMore (show more).
 */
export function createPagedList<T>(cfg: PagedListConfig<T>): PagedList {
  let items: T[] = [];
  let hasMore = false;
  let loading = false;

  function render(): void {
    if (items.length === 0) {
      patch(cfg.container, emptyDiv(
        hasMore ? cfg.emptyMessage : (cfg.emptyNoData || cfg.emptyMessage)));
      return;
    }
    const frag = cfg.renderItems(items);
    if (hasMore) {
      frag.appendChild(el('button', {
        type: 'button',
        className: 'more-btn',
        onclick: () => { loadMore(); }
      }, 'Show more\u2026'));
    }
    patch(cfg.container, frag);
  }

  async function reload(): Promise<void> {
    if (loading) return;
    loading = true;
    items = [];
    hasMore = false;
    try {
      const page = await cfg.fetchPage(0, cfg.pageSize);
      items = page.items;
      hasMore = page.hasMore;
      render();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      patch(cfg.container, errDiv(msg));
    } finally {
      loading = false;
    }
  }

  async function loadMore(): Promise<void> {
    if (loading || !hasMore) return;
    loading = true;
    const scrollPos = window.scrollY;
    try {
      const page = await cfg.fetchPage(items.length, cfg.pageSize);
      items = items.concat(page.items);
      hasMore = page.hasMore;
      render();
      window.scrollTo(0, scrollPos);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      patch(cfg.container, errDiv(msg));
    } finally {
      loading = false;
    }
  }

  return { reload, loadMore };
}
