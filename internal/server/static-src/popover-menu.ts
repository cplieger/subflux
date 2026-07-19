// popover-menu.ts — subflux's wiring of @cplieger/ui-primitives' createPopover
// for the two header dropdowns (the status popup + the user menu), replacing the
// native Popover API. createPopover positions the panel with JS (inline styles),
// so the old `08-popover.css` `@media (width < 600px)` switch between the
// content-sized desktop dropdown and the full-width flush mobile dropdown moves
// HERE: we pick `stretch: "viewport"` (full-bleed) vs content-sized from a
// matchMedia check and PATCH the live controller when the query flips via
// setOptions (an open panel repositions in place — no dispose-and-rebuild,
// no reopen dance).
//
// requires @cplieger/ui-primitives >= 2.2.0 (popover setOptions); verified
// locally via a node_modules overlay until released.

import { createPopover, type PlacementOptions } from "@cplieger/ui-primitives/popover";

// Full-width flush dropdown breakpoint. Matches the popover breakpoint the old
// `08-popover.css` used, NOT the 700px nav-button container-query breakpoint.
const NARROW_QUERY = "(width < 600px)";

// Desktop main-axis gap — reproduces the old `margin-block-start: var(--sp-3)`
// (0.375rem = 6px).
const DESKTOP_OFFSET = 6;

export interface MenuPopover {
  toggle(): void;
  hide(): void;
  readonly isOpen: boolean;
  /** Re-measure + re-clamp after a content change while open (no-op when
   *  closed). onOpen rebuilds already reposition automatically. */
  reposition(): void;
  dispose(): void;
}

interface MenuPopoverOptions {
  /** aria-haspopup advertised on the trigger — match the panel's role: "menu"
   *  for the user menu, the default `true` for the status group. */
  haspopup?: "menu" | true;
  /** Rebuild the panel's content each time it opens. */
  onOpen?: () => void;
}

/**
 * Wire a header trigger button + its panel into a dismissible dropdown.
 *
 * Desktop: content-sized, dropped below the button and right-aligned to it
 * (reproduces the old `top: anchor(end); right: anchor(right)` +
 * `margin-block-start: var(--sp-3)`).
 *
 * Mobile (< 600px): full-bleed `stretch: "viewport"` pinned flush to both
 * viewport edges (`margin: 0`) and dropped flush below the header bar. The main
 * axis stays anchored to the button (so createPopover keeps aria-expanded /
 * aria-haspopup on the button), but the offset is measured from the button's
 * bottom to the header's bottom so the panel sits exactly under the header's
 * bottom border — matching the old `position-anchor: --header-anchor;
 * top: anchor(end); left: 0; right: 0`.
 */
export function createMenuPopover(
  anchor: HTMLElement,
  panel: HTMLElement,
  opts: MenuPopoverOptions = {},
): MenuPopover {
  const narrow = window.matchMedia(NARROW_QUERY);
  const header = anchor.closest("header");

  // The breakpoint-dependent placement subset. Mobile: full-bleed, flush to
  // the viewport edges, and sitting flush below the header bar — the old
  // popover anchored to the HEADER, not the button, so the offset is measured
  // from the button's bottom to the header's bottom (ARIA stays on the button
  // while positioning as if anchored to the header). Desktop: content-sized,
  // gapped below the button. `stretch: undefined` in the desktop patch is the
  // documented setOptions idiom for clearing full-bleed mode.
  const placementFor = (): {
    [K in "stretch" | "offset" | "margin"]: PlacementOptions[K] | undefined;
  } => {
    if (!narrow.matches) {
      return { stretch: undefined, offset: DESKTOP_OFFSET, margin: 8 };
    }
    const offset = header
      ? Math.max(
          0,
          Math.round(header.getBoundingClientRect().bottom - anchor.getBoundingClientRect().bottom),
        )
      : 0;
    return { stretch: "viewport", offset, margin: 0 };
  };

  const initial = placementFor();
  const controller = createPopover(anchor, panel, {
    placement: "bottom",
    align: "end",
    ...(initial.offset !== undefined ? { offset: initial.offset } : {}),
    ...(initial.margin !== undefined ? { margin: initial.margin } : {}),
    ...(initial.stretch !== undefined ? { stretch: initial.stretch } : {}),
    haspopup: opts.haspopup ?? true,
    onOpen: (): void => {
      opts.onOpen?.();
      // The open hook typically rebuilds the panel's content (menu rebuild,
      // status skeleton), and placement ran BEFORE it — re-measure + re-clamp
      // against the real height, per the popover contract for content changes.
      controller.reposition();
    },
  });

  // Positioning is JS-driven, so the desktop/mobile switch is a matchMedia
  // handler, not a CSS media query: merge-patch the LIVE controller. An open
  // panel repositions immediately and keeps its dismissal state. Named (not
  // inline) so dispose() can remove it — an anonymous listener would outlive
  // the controller and patch a disposed one on the next breakpoint flip.
  const onBreakpointChange = (): void => {
    controller.setOptions(placementFor());
  };
  narrow.addEventListener("change", onBreakpointChange);

  return {
    toggle: (): void => {
      controller.toggle();
    },
    hide: (): void => {
      controller.hide();
    },
    get isOpen(): boolean {
      return controller.isOpen;
    },
    reposition: (): void => {
      controller.reposition();
    },
    dispose: (): void => {
      narrow.removeEventListener("change", onBreakpointChange);
      controller.dispose();
    },
  };
}
