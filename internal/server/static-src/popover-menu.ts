// popover-menu.ts — subflux's wiring of @cplieger/ui-primitives' createPopover
// for the two header dropdowns (the status popup + the user menu), replacing the
// native Popover API. createPopover positions the panel with JS (inline styles),
// so the old `08-popover.css` `@media (width < 600px)` switch between the
// content-sized desktop dropdown and the full-width flush mobile dropdown moves
// HERE: we pick `stretch: "viewport"` (full-bleed) vs content-sized from a
// matchMedia check and rebuild the controller when the query flips (both
// `stretch` and the measured mobile offset are fixed at construction time). The
// ui-primitives author flagged this exact migration point.
//
// requires @cplieger/ui-primitives >= 2.1.0 (popover stretch mode); verified
// locally via a node_modules overlay until released.

import { createPopover, type PopoverController } from "@cplieger/ui-primitives/popover";

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

  let controller = build();

  function build(): PopoverController {
    const stretched = narrow.matches;
    // Mobile: sit flush below the header bar. The old popover anchored to the
    // header, not the button; measuring button→header bottom keeps ARIA on the
    // button while positioning as if anchored to the header.
    let offset = DESKTOP_OFFSET;
    if (stretched) {
      offset = header
        ? Math.max(
            0,
            Math.round(
              header.getBoundingClientRect().bottom - anchor.getBoundingClientRect().bottom,
            ),
          )
        : 0;
    }
    return createPopover(anchor, panel, {
      placement: "bottom",
      align: "end",
      offset,
      margin: stretched ? 0 : 8,
      haspopup: opts.haspopup ?? true,
      ...(stretched ? { stretch: "viewport" as const } : {}),
      ...(opts.onOpen ? { onOpen: opts.onOpen } : {}),
    });
  }

  // Positioning is JS-driven, so the desktop/mobile switch is a matchMedia
  // handler, not a CSS media query. `stretch` and the measured offset are fixed
  // at construction, so rebuild the controller when the query flips; reopen if
  // it was open so a resize across the breakpoint doesn't drop the panel.
  narrow.addEventListener("change", () => {
    const wasOpen = controller.isOpen;
    controller.dispose();
    controller = build();
    if (wasOpen) {
      controller.show();
    }
  });

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
    dispose: (): void => {
      controller.dispose();
    },
  };
}
