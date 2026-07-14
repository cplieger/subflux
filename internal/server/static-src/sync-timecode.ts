// sync-timecode.ts — Timecode input widget extracted from sync.ts.

import { el, icon } from "./dom.js";

// TimecodeInput extends HTMLElement with custom methods attached at runtime.
export interface TimecodeInput extends HTMLElement {
  handleKey: (e: KeyboardEvent) => void;
  setValue: (newMs: number) => void;
  /** Stop any pending hold-repeat timers. Caller MUST invoke when the
   *  widget is removed from the DOM (e.g. dialog close) to prevent a
   *  pending tick from firing on a detached element. Idempotent. */
  dispose: () => void;
}

export function formatOffsetMs(ms: number): string {
  if (ms === 0) {
    return "0.000s";
  }
  const sign = ms > 0 ? "+" : "-";
  const abs = Math.abs(ms);
  const sec = Math.floor(abs / 1000);
  const frac = abs % 1000;
  return `${sign}${sec}.${String(frac).padStart(3, "0")}s`;
}

// Hold-to-repeat: fires adjust on press, then accelerates.
// Starts at 400ms interval, decreases to 50ms minimum.
//
// Returns a `dispose` function the caller MUST invoke when the button
// is removed from the DOM (e.g. dialog close). Without disposal, a
// pending tick can fire on a detached element after a slow animation
// frame, calling adjust() with a stale closure. The dispose() is
// idempotent — safe to call multiple times.
function holdRepeat(
  btn: HTMLElement,
  getDelta: () => number,
  adjust: (delta: number) => void,
): () => void {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let delay = 400;

  function tick(): void {
    adjust(getDelta());
    delay = Math.max(50, delay * 0.75);
    timer = setTimeout(tick, delay);
  }

  function start(e: Event): void {
    e.preventDefault();
    adjust(getDelta());
    delay = 400;
    timer = setTimeout(tick, delay);
  }

  function stop(): void {
    if (timer != null) {
      clearTimeout(timer);
    }
    timer = null;
  }

  btn.addEventListener("mousedown", start);
  btn.addEventListener("mouseup", stop);
  btn.addEventListener("mouseleave", stop);
  btn.addEventListener("touchstart", start, { passive: false });
  btn.addEventListener("touchend", stop);
  btn.addEventListener("touchcancel", stop);

  return stop;
}

// Touch drag: vertical swipe adjusts value. Tracks cumulative distance
// and fires adjust() every 20px of movement.
function addTouchDrag(seg: HTMLElement, delta: number, adjust: (delta: number) => void): void {
  let startY = 0;
  let accumulated = 0;
  const threshold = 20;

  seg.addEventListener(
    "touchstart",
    (e: TouchEvent) => {
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- touch event always has touches[0]
      startY = e.touches[0]!.clientY;
      accumulated = 0;
    },
    { passive: true },
  );

  seg.addEventListener(
    "touchmove",
    (e: TouchEvent) => {
      e.preventDefault();
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- touch event always has touches[0]
      const dy = startY - e.touches[0]!.clientY;
      const steps = Math.trunc((dy - accumulated) / threshold);
      if (steps !== 0) {
        accumulated += steps * threshold;
        adjust(steps * delta);
      }
    },
    { passive: false },
  );
}

// Build a segmented timecode input: [±] [S] . [H] [T] [O] s
// Each segment is focusable; arrow keys and scroll adjust the value.
// Chevron buttons adjust the currently focused segment.
// onChange(newMs) is called on every adjustment.
export function buildTimecodeInput(
  initialMs: number,
  onChange: (newMs: number) => void,
): HTMLElement {
  let ms = initialMs;
  let activeSeg: { seg: HTMLElement; delta: number } | null = null;

  interface Decomposed {
    sign: string;
    sec: string;
    h: string;
    t: string;
    o: string;
  }

  function decompose(v: number): Decomposed {
    const sign = v >= 0 ? "+" : "-";
    const abs = Math.abs(v);
    const sec = Math.floor(abs / 1000);
    const frac = abs % 1000;
    return {
      sign,
      sec: String(sec),
      h: String(Math.floor(frac / 100)),
      t: String(Math.floor((frac % 100) / 10)),
      o: String(frac % 10),
    };
  }

  function refresh(): void {
    const d = decompose(ms);
    signEl.textContent = d.sign;
    secEl.textContent = d.sec;
    hEl.textContent = d.h;
    tEl.textContent = d.t;
    oEl.textContent = d.o;
    // Keep the spinbutton contract honest: every segment announces the
    // current total offset (the value the arrows actually change).
    for (const seg of [secEl, hEl, tEl, oEl]) {
      seg.setAttribute("aria-valuenow", String(ms));
      seg.setAttribute("aria-valuetext", `${(ms / 1000).toFixed(3)} seconds`);
    }
  }

  function adjust(delta: number): void {
    ms += delta;
    refresh();
    onChange(ms);
  }

  function setActive(seg: HTMLElement, delta: number): void {
    if (activeSeg) {
      activeSeg.seg.classList.remove("tc-active");
    }
    activeSeg = { seg, delta };
    seg.classList.add("tc-active");
  }

  function handleWheel(e: WheelEvent, delta: number): void {
    e.preventDefault();
    adjust(e.deltaY < 0 ? delta : -delta);
  }

  function wireSeg(seg: HTMLElement, delta: number): void {
    seg.addEventListener("mousedown", (e: MouseEvent) => {
      e.preventDefault();
    });
    seg.addEventListener("click", () => {
      setActive(seg, delta);
    });
    // Real DOM focus selects the segment, so Tab reaches every magnitude and
    // the existing ArrowUp/Down handler operates on it — selection was
    // previously click-only, stranding keyboard users on the default 1ms.
    seg.addEventListener("focus", () => {
      setActive(seg, delta);
    });
    seg.addEventListener(
      "wheel",
      (e: WheelEvent) => {
        handleWheel(e, delta);
      },
      { passive: false },
    );
    addTouchDrag(seg, delta, adjust);
  }

  const segAttrs = (label: string): Record<string, string> => ({
    className: "tc-seg",
    role: "spinbutton",
    tabindex: "0",
    "aria-label": label,
  });

  const toggleSign = (): void => {
    ms = -ms;
    refresh();
    onChange(ms);
  };
  const signEl = el("span", {
    ...segAttrs("Sign"),
    className: "tc-seg tc-sign",
    role: "button",
    onclick: toggleSign,
    onkeydown: (e: KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        toggleSign();
      }
    },
  });

  const secEl = el("span", segAttrs("Seconds"));
  wireSeg(secEl, 1000);

  const hEl = el("span", segAttrs("100ms"));
  wireSeg(hEl, 100);

  const tEl = el("span", segAttrs("10ms"));
  wireSeg(tEl, 10);

  const oEl = el("span", segAttrs("1ms"));
  wireSeg(oEl, 1);

  // 1ms is the default active segment.
  oEl.classList.add("tc-active");
  activeSeg = { seg: oEl, delta: 1 };

  refresh();

  const upBtn = el(
    "button",
    {
      type: "button",
      className: "tc-chevron",
      "aria-label": "Increase",
    },
    icon("chevron-up"),
  );

  const downBtn = el(
    "button",
    {
      type: "button",
      className: "tc-chevron",
      "aria-label": "Decrease",
    },
    icon("chevron-down"),
  );

  const stopUp = holdRepeat(upBtn, () => (activeSeg ? activeSeg.delta : 0), adjust);
  const stopDown = holdRepeat(downBtn, () => (activeSeg ? -activeSeg.delta : 0), adjust);

  const btnStack = el("div", { className: "tc-btn-stack" }, upBtn, downBtn);

  const container = el(
    "div",
    {
      className: "sync-timecode",
      id: "sync-offset-val",
    },
    el(
      "div",
      { className: "tc-numbers" },
      signEl,
      secEl,
      el("span", { className: "tc-dot" }, "."),
      hEl,
      tEl,
      oEl,
      el("span", { className: "tc-unit" }, "s"),
    ),
    btnStack,
  );

  // Keyboard: arrow keys adjust the active segment from anywhere in the dialog.
  (container as TimecodeInput).handleKey = (e: KeyboardEvent): void => {
    if (!activeSeg) {
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      adjust(activeSeg.delta);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      adjust(-activeSeg.delta);
    }
  };

  // Public method to set value externally (audio sync, reset, dropdown change).
  (container as TimecodeInput).setValue = (newMs: number): void => {
    ms = newMs;
    refresh();
  };

  // Dispose: stop pending hold-repeat timers. Called by sync.ts on dialog
  // close so a detached element can't keep ticking.
  (container as TimecodeInput).dispose = (): void => {
    stopUp();
    stopDown();
  };

  return container;
}

// Update the timecode display from external sources (audio sync, subtitle switch).
export function updateTimecodeDisplay(newMs: number): void {
  const tc = document.getElementById("sync-offset-val") as TimecodeInput | null;
  if (tc) {
    tc.setValue(newMs);
  }
}
