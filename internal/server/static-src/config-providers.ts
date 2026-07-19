// config-providers.ts — Provider-section renderers extracted from config.ts.

import { el } from "./dom.js";
import { createDisclosure } from "@cplieger/ui-primitives/disclosure";
import { providerTimeouts } from "./wire/client.gen.js";
import { cfgProviderBlock, scalarString } from "./config-values.js";
import type { SchemaSection } from "./api-types.js";
import { renderField, cfgField, cfgToggle } from "./config-renderers.js";

// --- Inline interfaces for provider API shapes ---

// --- Provider section renderers ---

export function renderProvidersSection(schema: SchemaSection): HTMLElement {
  const sec = el("div", { className: "cfg-section" });
  sec.appendChild(el("div", { className: "cfg-title" }, schema.title));

  for (const prov of schema.providers ?? []) {
    const block = cfgProviderBlock(prov.name);
    const card = el("div", { className: "provider" });
    // A present block without `enabled: false` counts as enabled; a missing
    // block is disabled (same as the old per-provider text blocks).
    const isEnabled = block !== undefined && block.enabled !== false;

    const headerItems: HTMLElement[] = [el("span", null, prov.label)];
    headerItems.push(cfgToggle(`cfg-prov-${prov.name}-enabled`, isEnabled));
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
        details.appendChild(renderField(fid, sf, scalarString(settings?.[sf.key])));
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
    sec.appendChild(card);
  }

  // Async: fetch provider health and show status badges.
  providerTimeouts()
    .then((data) => {
      if (!data?.providers) {
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
        const badge = el(
          "span",
          {
            className: "badge badge-health",
            "data-status": status.timed_out ? "err" : "ok",
          },
          status.timed_out ? (status.last_error ?? "timed out") : "healthy",
        );
        head.insertBefore(badge, head.lastElementChild);
      }
    })
    .catch(() => {
      /* ignore */
    });

  return sec;
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
  const providers: Record<string, unknown> = {};
  for (const prov of schema.providers ?? []) {
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
    if (prov.settings && prov.settings.length > 0) {
      const settings: Record<string, unknown> = {};
      for (const sf of prov.settings) {
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
      block["settings"] = settings;
    }
    providers[prov.name] = block;
  }
  return providers;
}
