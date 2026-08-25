// Connection test control for a Sonarr/Radarr config section.
//
// Shared by both bundles on purpose: the settings dialog (app.js) and the setup
// wizard (login.js) ask the same question about the same two fields, and the
// wizard is where it matters most — without it a bad URL typed on step one is
// only discovered by the save at the end of the walk. esbuild's splitting puts
// this in the chunk both entries already share, so the second host costs
// nothing.
//
// It reports INLINE rather than through a toast or a page banner. The wizard has
// no toaster at all, and in the settings dialog the arr sections sit inside a
// scrolling dialog where a banner pinned to the top would be off-screen from the
// button that produced it. Local feedback is the only shape that reads the same
// in both places — and it keeps this module out of notify.ts's dependency cone,
// which would otherwise drag the toast primitive into the login bundle.

import { el } from "./dom.js";
import { testArrConnectionRaw } from "./wire/client.gen.js";

/** Fields the test reads. Elements rather than ids: both hosts build the
 *  control while their section is still a detached subtree, where
 *  getElementById cannot reach but a scoped querySelector can. */
export interface ArrTestFields {
  url: HTMLInputElement | null;
  apiKey: HTMLInputElement | null;
}

const LABEL_IDLE = "Test connection";
const LABEL_BUSY = "Testing\u2026";

/** Build the test-connection control for one arr section. `kind` is the config
 *  section key ("sonarr" / "radarr"), which is also what the endpoint takes. */
export function arrTestControl(kind: string, fields: ArrTestFields): HTMLElement {
  const status = el("span", {
    className: "arr-test-status",
    // Polite, not assertive: the operator asked for this result and is looking
    // at the button, so it needs announcing without interrupting. The wizard's
    // shared #wizardError slot is role="alert" precisely because nobody asked
    // for what lands in it.
    role: "status",
  });

  const btn = el(
    "button",
    { type: "button", className: "arr-test-btn" },
    LABEL_IDLE,
  ) as HTMLButtonElement;

  let pending: AbortController | null = null;

  // A verdict outlives the values it was about unless something clears it: a
  // green "Connected" sitting next to a URL edited since the test is a lie the
  // operator has no way to spot. Any keystroke in either field retires it.
  const invalidate = (): void => {
    pending?.abort();
    pending = null;
    reset();
  };
  const reset = (): void => {
    btn.removeAttribute("data-status");
    btn.removeAttribute("aria-busy");
    btn.disabled = false;
    btn.textContent = LABEL_IDLE;
    status.textContent = "";
    status.removeAttribute("data-status");
  };
  fields.url?.addEventListener("input", invalidate);
  fields.apiKey?.addEventListener("input", invalidate);

  const settle = (state: "ok" | "err", msg: string): void => {
    btn.dataset["status"] = state;
    status.dataset["status"] = state;
    status.textContent = msg;
  };

  btn.addEventListener("click", () => {
    // No abort of an in-flight request here: the button is disabled for the
    // duration, so a second click cannot land. `pending` is aborted only by
    // invalidate(), which re-enables the button — and a click after THAT still
    // cannot report over the new one, because the old run checks its own signal.
    const ctl = new AbortController();
    pending = ctl;

    btn.removeAttribute("data-status");
    status.removeAttribute("data-status");
    btn.disabled = true;
    btn.setAttribute("aria-busy", "true");
    btn.textContent = LABEL_BUSY;
    status.textContent = "";

    // An empty api_key is sent as-is: the server reads "keep what you have"
    // exactly as a save does, because a saved secret is rendered as an empty
    // field (the redacting GET never ships the value) and demanding a retype
    // would fail the test on precisely the configs that work.
    const body = {
      kind,
      url: fields.url?.value.trim() ?? "",
      api_key: fields.apiKey?.value ?? "",
    };

    void (async (): Promise<void> => {
      try {
        const res = await testArrConnectionRaw(body, { signal: ctl.signal });
        if (ctl.signal.aborted) {
          return;
        }
        btn.disabled = false;
        btn.removeAttribute("aria-busy");
        btn.textContent = LABEL_IDLE;
        if (!res.ok || !res.data) {
          // Transport or envelope failure, not a verdict about the arr: an
          // expired session or a 500 lands here and must not read as
          // "your URL is wrong".
          settle("err", res.error ?? "Test request failed");
          return;
        }
        if (res.data.valid) {
          settle("ok", "Connected");
          return;
        }
        settle("err", res.data.error ?? "Not reachable");
      } finally {
        if (pending === ctl) {
          pending = null;
        }
      }
    })();
  });

  return el("div", { className: "arr-test" }, btn, status);
}
