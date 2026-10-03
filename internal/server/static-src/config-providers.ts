// config-providers.ts — Provider-section renderers extracted from config.ts.

import { el } from "./dom.js";
import { createDisclosure } from "@cplieger/ui-primitives/disclosure";
import { providerTimeouts } from "./wire/client.gen.js";
import { cfgProviderBlock, cfgUnrendered, scalarString } from "./config-values.js";
import { mountConnTest } from "./conn-test.js";
import { configBanner } from "./config-banner.js";
import type { ProviderSchema, SchemaField, SchemaSection } from "./api-types.js";
import type { ProviderStatus } from "./wire/types.gen.js";
import { renderField, cfgField, cfgToggle } from "./config-renderers.js";

// --- Inline interfaces for provider API shapes ---

// --- Provider section renderers ---

/** providerFieldValue resolves what a provider setting renders with.
 *
 *  An ABSENT key falls back to the schema default, matching both the server
 *  (provider.NormalizeSettings fills a declared-but-absent field from its
 *  Default before the factory runs) and the wizard (which already reads
 *  `f.default`). Without the fallback a field whose default is `true` rendered
 *  unchecked, and saving the dialog then wrote that false back as the user's
 *  choice, silently turning the setting off.
 *
 *  A PRESENT value is the user's, including `null` from a bare `key:` in YAML:
 *  the server treats a present-but-null key as set too, so substituting the
 *  default here would disagree with the value the engine actually uses. */
export function providerFieldValue(
  field: SchemaField,
  settings: Record<string, unknown> | undefined,
): string {
  const raw = settings?.[field.key];
  if (raw === undefined) {
    return field.default ?? "";
  }
  return scalarString(raw);
}

/** Teardown marker for the health fetch, which outlives the render pass that
 *  started it: the next pass aborts the previous one, whose section is detached
 *  by then and whose badges would land nowhere. */
let healthPass: AbortController | null = null;

export function renderProvidersSection(schema: SchemaSection): HTMLElement {
  const sec = el("div", { className: "cfg-section" });
  sec.appendChild(el("div", { className: "cfg-title" }, schema.title));

  for (const prov of schema.providers ?? []) {
    const block = cfgProviderBlock(prov.name);
    const card = el("div", { className: "provider" });
    // A present block without `enabled: false` counts as enabled; a missing
    // block is disabled (same as the old per-provider text blocks).
    const isEnabled = block !== undefined && block.enabled !== false;

    // A <label for> rather than a span: the .toggle wrapper holds only the
    // slider, so the provider name is the enable checkbox's only possible
    // accessible name (axe `label`, critical, one node per provider).
    const toggleId = `cfg-prov-${prov.name}-enabled`;
    const headerItems: HTMLElement[] = [el("label", { for: toggleId }, prov.label)];
    headerItems.push(cfgToggle(toggleId, isEnabled));
    card.appendChild(el("div", { className: "provider-head" }, ...headerItems));

    // Region-only disclosure (the header checkbox is the visible control):
    // animated height + aria-hidden/inert instead of the display:none snap.
    const details = el("div", { className: "provider-body" });
    const detailsCtl = createDisclosure(null, details, { open: isEnabled });

    const priVal = block?.priority !== undefined ? String(block.priority) : "";
    details.appendChild(
      cfgField(
        `cfg-prov-${prov.name}-priority`,
        "Priority",
        "number",
        priVal,
        "99",
        "Tiebreaker when subtitles have equal scores. Lower = more trusted.",
      ),
    );

    if (prov.settings) {
      const settings = block?.settings;
      for (const sf of prov.settings) {
        const fid = `cfg-prov-${prov.name}-s-${sf.key}`;
        details.appendChild(renderField(fid, sf, providerFieldValue(sf, settings)));
      }
    }

    card.appendChild(details);

    const toggle = card.querySelector<HTMLInputElement>(`#cfg-prov-${prov.name}-enabled`);
    if (toggle) {
      toggle.addEventListener("change", () => {
        if (toggle.checked) {
          detailsCtl.open();
        } else {
          detailsCtl.close();
        }
      });
    }
    appendProviderConnTest(card, prov);
    sec.appendChild(card);
  }

  // Async: fetch provider health and show status badges.
  healthPass?.abort();
  const pass = new AbortController();
  healthPass = pass;
  providerTimeouts({ signal: pass.signal })
    .then((data) => {
      // An abort that arrives after the response is already in hand still has
      // to stop the write.
      if (pass.signal.aborted || !data?.providers) {
        return;
      }
      for (const [name, status] of Object.entries(data.providers)) {
        const header = sec.querySelector(`#${CSS.escape(`cfg-prov-${name}-enabled`)}`);
        if (!header) {
          continue;
        }
        const card = header.closest(".provider");
        if (!card) {
          continue;
        }
        const head = card.querySelector(".provider-head");
        if (!head) {
          continue;
        }
        const health = providerHealth(status, Date.now());
        const badge = el(
          "span",
          {
            className: "badge badge-health",
            "data-status": health.status,
            ...(health.tip ? { "data-tip": health.tip } : {}),
          },
          health.text,
        );
        // Anchored on the toggle rather than on lastElementChild: the head's
        // last element is whatever was appended most recently, so a positional
        // insert silently changes where the badge lands the moment the head
        // grows another control.
        head.insertBefore(badge, head.querySelector(".toggle"));
      }
    })
    .catch(() => {
      /* ignore */
    });

  return sec;
}

interface ProviderHealth {
  status: "ok" | "warn" | "err";
  text: string;
  tip?: string;
}

/** providerHealth names the one state a provider card's badge shows, most
 *  severe first. `paused_for` is nanoseconds, like every duration on the wire. */
export function providerHealth(status: ProviderStatus, now: number): ProviderHealth {
  if (status.disabled) {
    return {
      status: "err",
      text: "disabled: credentials rejected",
      ...(status.disabled_reason ? { tip: status.disabled_reason } : {}),
    };
  }
  if (status.timed_out) {
    return { status: "err", text: status.last_error ?? "timed out" };
  }
  if (status.paused_for !== undefined && status.paused_for > 0) {
    const until = new Date(now + status.paused_for / 1e6);
    const hhmm = `${String(until.getHours()).padStart(2, "0")}:${String(until.getMinutes()).padStart(2, "0")}`;
    return { status: "warn", text: `paused until ${hhmm}` };
  }
  if (status.rejected_settings && status.rejected_settings.length > 0) {
    return { status: "warn", text: `setting rejected: ${status.rejected_settings.join(", ")}` };
  }
  return { status: "ok", text: "healthy" };
}

// appendProviderConnTest adds the credential-check control to a provider card
// that declares one, in the card head beside the toggle — the same placement the
// arr sections use for theirs.
//
// Every rendered setting is sent, not just the secrets: the server reads what it
// needs by name, and the values it does not need cost one small JSON field each.
function appendProviderConnTest(card: HTMLElement, prov: ProviderSchema): void {
  if (!prov.conn_test) {
    return;
  }
  const inputs: Record<string, HTMLInputElement | null> = {};
  for (const sf of prov.settings ?? []) {
    inputs[sf.key] = card.querySelector<HTMLInputElement>(
      `#${CSS.escape(`cfg-prov-${prov.name}-s-${sf.key}`)}`,
    );
  }
  const head = card.querySelector(".provider-head");
  if (head) {
    mountConnTest(head, prov.name, { inputs, banner: configBanner });
  }
}

// settingScalar mirrors the YAML scalar inference the old text emitter got
// for free: a numeric-looking value becomes a number (flexint-style settings
// stayed ints through the YAML round-trip), "true"/"false" become booleans,
// empty stays "" (the old emitter wrote a literal ""), anything else stays a
// string.
function settingScalar(v: string): unknown {
  if (v === "true") {
    return true;
  }
  if (v === "false") {
    return false;
  }
  if (v !== "" && /^-?\d+(\.\d+)?$/.test(v)) {
    return Number(v);
  }
  return v;
}

// genProviders builds the providers section for the structured save,
// mirroring what the old YAML emitter produced: priority only when set,
// settings always emitted per rendered field. Empty secret settings ride
// as "" so the server merges the stored value (schema-driven).
export function genProviders(schema: SchemaSection): Record<string, unknown> {
  const rendered = schema.providers ?? [];
  const providers = cfgUnrendered([schema.key], new Set(rendered.map((p) => p.name)));
  for (const prov of rendered) {
    const block: Record<string, unknown> = {};
    const enEl = document.getElementById(
      `cfg-prov-${prov.name}-enabled`,
    ) as HTMLInputElement | null;
    block["enabled"] = enEl ? enEl.checked : false;
    const priEl = document.getElementById(
      `cfg-prov-${prov.name}-priority`,
    ) as HTMLInputElement | null;
    if (priEl?.value) {
      const pri = Number(priEl.value);
      block["priority"] = Number.isFinite(pri) ? pri : priEl.value;
    }
    const settings = cfgUnrendered(
      [schema.key, prov.name, "settings"],
      new Set((prov.settings ?? []).map((sf) => sf.key)),
    );
    for (const sf of prov.settings ?? []) {
      const fEl = document.getElementById(
        `cfg-prov-${prov.name}-s-${sf.key}`,
      ) as HTMLInputElement | null;
      if (!fEl) {
        continue;
      }
      if (fEl.type === "checkbox") {
        settings[sf.key] = fEl.checked;
      } else {
        // Covers empty secrets too: "" tells the server to keep the
        // stored secret value.
        settings[sf.key] = settingScalar(fEl.value);
      }
    }
    if ((prov.settings ?? []).length > 0 || Object.keys(settings).length > 0) {
      block["settings"] = settings;
    }
    providers[prov.name] = block;
  }
  return providers;
}
