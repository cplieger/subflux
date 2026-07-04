// skeleton-timing.ts — anti-flicker timing for a "show a skeleton, then
// replace it with content" load.
//
// Two flickers are avoided:
//   - a fast load flashing the skeleton for a couple of frames, and
//   - a medium load showing the skeleton then yanking it away almost at once.
//
// Usage:
//   const t = skeletonTiming(() => patch(out, skeleton), { signal });
//   await fetchThing(signal);
//   t.commit(() => patch(out, content));

interface SkeletonTimingOptions {
  /** Delay before the skeleton is painted (default 150ms). A load that settles
   *  within this window never paints the skeleton at all. */
  showDelayMs?: number;
  /** Minimum time the skeleton stays up once painted (default 300ms), so it
   *  never appears then instantly vanishes. */
  minVisibleMs?: number;
  /** Abort signal for the underlying load. If it fires before the skeleton is
   *  painted, the skeleton is suppressed — no orphaned skeleton for a cancelled
   *  or superseded load. */
  signal?: AbortSignal;
}

interface SkeletonTimingHandle {
  /** Paint the content. Call once, after the awaited work settles. Honors
   *  show-delay (renders immediately when the skeleton was never shown) and
   *  min-visible (defers until the skeleton has been up long enough). */
  commit(render: () => void): void;
}

/**
 * Build an anti-flicker controller. `show` paints the skeleton (deferred by
 * show-delay); the returned `commit(render)` paints the content, honoring
 * show-delay and min-visible.
 *
 * The abort signal governs only the throwaway skeleton: if it fires before the
 * show-delay elapses, the skeleton is never painted. `commit`'s render always
 * runs — a caller whose content is a captured, stale-sensitive result should
 * guard its own render closure against the signal (subflux's `.then` sites
 * already do `if (signal.aborted) return`); an idempotent, live-data render
 * such as coverage.ts's `ensureMounted` is safe to run unconditionally.
 */
export function skeletonTiming(
  show: () => void,
  opts: SkeletonTimingOptions = {},
): SkeletonTimingHandle {
  const showDelayMs = opts.showDelayMs ?? 150;
  const minVisibleMs = opts.minVisibleMs ?? 300;
  let shownAt: number | null = null;
  const showTimer = setTimeout(() => {
    if (!opts.signal?.aborted) {
      shownAt = Date.now();
      show();
    }
  }, showDelayMs);
  return {
    commit(render: () => void): void {
      clearTimeout(showTimer);
      const at = shownAt;
      if (at === null) {
        // Skeleton never painted (fast load, or suppressed by abort) — paint
        // the content straight away.
        render();
        return;
      }
      const remaining = minVisibleMs - (Date.now() - at);
      if (remaining <= 0) {
        render();
      } else {
        setTimeout(render, remaining);
      }
    },
  };
}
