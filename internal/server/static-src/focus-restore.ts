// focus-restore.ts — reader state across a destructive re-render.

/** Identifies a control across a rebuild, so a container installed with
 *  `replaceChildren` can hand the keyboard back to the one the reader was on. */
export const FOCUS_KEY = "data-focus-key";

const tracked = new WeakMap<HTMLElement, { key: string | null }>();

/** Track focus inside a container whose re-render can be triggered from behind
 *  a modal. Call once per container lifetime.
 *
 *  Opt in where a re-render can run while a sub-dialog still holds focus: `ask`
 *  resolves roughly 200 ms before its dialog closes, so `document.activeElement`
 *  at capture time points inside that dialog rather than at the control the
 *  reader left. */
export function trackFocus(container: HTMLElement): void {
  const state = { key: null as string | null };
  tracked.set(container, state);
  container.addEventListener("focusin", (ev) => {
    if (ev.target instanceof HTMLElement) {
      state.key = ev.target.getAttribute(FOCUS_KEY) ?? state.key;
    }
  });
}

/** Capture reader state before a destructive install; call the returned
 *  function after it.
 *
 *  Pass `scrollEl` as a function when the install REPLACES the scroller: it is
 *  resolved once to read the offset and again to write it, so the value lands
 *  on the incoming element rather than on a detached one. An element is the
 *  form for a scroller that survives. */
export function captureReaderState(
  container: HTMLElement,
  opts?: { scrollEl?: HTMLElement | (() => HTMLElement | null) | null },
): () => void {
  const state = tracked.get(container);
  const key = state === undefined ? activeKey() : state.key;
  const scrollEl = opts?.scrollEl ?? null;
  const resolveScroll = (): HTMLElement | null =>
    typeof scrollEl === "function" ? scrollEl() : scrollEl;
  const scrollTop = resolveScroll()?.scrollTop ?? 0;

  return () => {
    const target =
      key === null ? null : container.querySelector<HTMLElement>(`[${FOCUS_KEY}="${key}"]`);
    target?.focus();
    // After the focus, which can scroll a container to bring its target in view.
    const scroller = resolveScroll();
    if (scroller !== null) {
      scroller.scrollTop = scrollTop;
    }
    if (target === null || document.activeElement === target) {
      return;
    }
    // A modal above the container makes its whole subtree inert, so focus() was
    // refused; claim it when that dialog closes, after the platform has handed
    // focus back to an opener this render has already discarded.
    const blocker = document.activeElement?.closest("dialog");
    blocker?.addEventListener(
      "close",
      () => {
        target.focus();
      },
      { once: true },
    );
  };
}

function activeKey(): string | null {
  const active = document.activeElement;
  return active instanceof HTMLElement ? active.getAttribute(FOCUS_KEY) : null;
}
