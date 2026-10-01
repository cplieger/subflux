// Credential-check control for a config section or provider. Which sections
// carry it is schema-declared, and the probe runs server-side; see `subflux.md`.

import { el, icon } from "./dom.js";
import { showError, hideError, bannerText } from "./dom-core.js";
import { testConnectionRaw } from "./wire/client.gen.js";
import type { ApiResult } from "./api-client.js";
import type { ConnTestResponse } from "./wire/types.gen.js";

/** How long a green verdict stays before the control returns to idle. A failure
 *  never fades: its text sits in a banner the operator has to be able to read. */
const SUCCESS_LINGER_MS = 3000;

const LABEL = "Test credentials";

/** The control's host: the inputs whose values it sends and whose edits retire a
 *  verdict, and the id of the surface's red top banner.
 *
 *  Elements rather than ids, because every host builds the control while its
 *  section is still detached. A null element sends "" for its key. */
export interface ConnTestHost {
  inputs: Record<string, HTMLInputElement | null>;
  bannerId: string;
}

/** Build the credential-check control for one section or provider. `kind` is the
 *  config section key, which for a provider is its name. */
export function connTestControl(kind: string, host: ConnTestHost): HTMLButtonElement {
  const btn = el("button", {
    type: "button",
    // Two classes so the styling outranks each host's own `button` rule
    // without restating the height that rule supplies.
    className: "conn-test conn-test-btn",
    "aria-label": LABEL,
    "data-tip": LABEL,
  }) as HTMLButtonElement;

  let pending: AbortController | null = null;
  // Cancellable, or a fade from the previous verdict overwrites this one.
  let fade: ReturnType<typeof setTimeout> | null = null;
  // What this control last put in the shared banner, so clearing cannot wipe a
  // message another control wrote after it.
  let posted = "";

  const idle = (): void => {
    btn.removeAttribute("data-status");
    btn.removeAttribute("aria-busy");
    btn.disabled = false;
    btn.replaceChildren(icon("flask"));
  };

  const cancelFade = (): void => {
    if (fade !== null) {
      clearTimeout(fade);
      fade = null;
    }
  };

  const clearBanner = (): void => {
    if (posted !== "" && bannerText(host.bannerId) === posted) {
      hideError(host.bannerId);
    }
    posted = "";
  };

  const settleOK = (): void => {
    btn.dataset["status"] = "ok";
    btn.replaceChildren(icon("check"));
    clearBanner();
    fade = setTimeout(() => {
      fade = null;
      idle();
    }, SUCCESS_LINGER_MS);
  };

  const settleError = (msg: string): void => {
    btn.dataset["status"] = "err";
    btn.replaceChildren(icon("close"));
    posted = msg;
    showError(host.bannerId, msg);
  };

  // A green verdict beside a key edited since the test is a lie the operator
  // cannot spot, so any keystroke in a watched field retires it.
  const invalidate = (): void => {
    pending?.abort();
    pending = null;
    cancelFade();
    clearBanner();
    idle();
  };
  for (const input of Object.values(host.inputs)) {
    input?.addEventListener("input", invalidate);
  }

  btn.addEventListener("click", () => {
    // Nothing is aborted here: the button is disabled for the duration. Only
    // invalidate() aborts, and the run it abandons re-checks its own signal.
    const ctl = new AbortController();
    pending = ctl;
    cancelFade();
    clearBanner();

    btn.removeAttribute("data-status");
    btn.disabled = true;
    btn.setAttribute("aria-busy", "true");
    btn.replaceChildren(el("span", { className: "spinner" }));

    // Empty secrets are sent as-is; the server reads them as "keep what you
    // have", as a save does, since a stored secret renders as an empty field.
    const settings: Record<string, string> = {};
    for (const [key, input] of Object.entries(host.inputs)) {
      settings[key] = input?.value ?? "";
    }

    void (async (): Promise<void> => {
      try {
        const res = await testConnectionRaw({ kind, settings }, { signal: ctl.signal });
        if (ctl.signal.aborted) {
          return;
        }
        btn.disabled = false;
        btn.removeAttribute("aria-busy");
        const failure = failureOf(res);
        if (failure === null) {
          settleOK();
          return;
        }
        settleError(failure);
      } finally {
        if (pending === ctl) {
          pending = null;
        }
      }
    })();
  });

  idle();
  return btn;
}

/** failureOf reduces one answer to the message to report, or null when the
 *  credentials were accepted.
 *
 *  A transport or envelope failure is reported too, and deliberately not as a
 *  verdict about the service: an expired session or a 500 must not read as "your
 *  key is wrong", so it carries the transport's own words. */
function failureOf(res: ApiResult<ConnTestResponse>): string | null {
  if (!res.ok || !res.data) {
    return res.error ?? "Test request failed";
  }
  if (res.data.valid) {
    return null;
  }
  return res.data.error ?? "The credentials were not accepted";
}

/** mountConnTest places the control in a section or card header, BEFORE the
 *  header's toggle: appending moved the toggle inward, so a card with
 *  credentials and one without had their toggles in different places. A header
 *  with no toggle takes the control last. */
export function mountConnTest(header: Element, kind: string, host: ConnTestHost): void {
  const control = connTestControl(kind, host);
  const toggle = header.querySelector(".toggle, .wiz-toggle");
  if (toggle) {
    header.insertBefore(control, toggle);
    return;
  }
  header.appendChild(control);
}
