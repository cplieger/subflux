// config-providers.ts — Provider-section renderers extracted from config.ts.

import { el } from "./dom.js";
import { apiGet } from "./api-client.js";
import { extractYAMLValue, extractYAMLBlock, parseProviderBlocks } from "./config-yaml.js";
import type { ProviderSchema, ConfigSchema } from "./api-types.js";
import { renderField, cfgField, cfgToggle } from "./config-renderers.js";
import { EMBEDDED_PROVIDER } from "./constants.js";

// --- Inline interfaces for provider API shapes ---

interface ProviderHealthStatus {
  timed_out: boolean;
  last_error?: string;
}

interface ProviderHealthResponse {
  providers?: Record<string, ProviderHealthStatus>;
}

// --- Provider section renderers ---

export function renderProvidersSection(
  schema: ConfigSchema,
  sections: Record<string, string>,
): HTMLElement {
  const sec = el("div", { className: "cfg-section" });
  sec.appendChild(el("div", { className: "cfg-title" }, schema.title));
  const raw = sections["providers"] ?? "";
  const blocks = parseProviderBlocks(raw);

  for (const prov of schema.providers) {
    if (prov.name === EMBEDDED_PROVIDER) {
      continue;
    }
    const block = blocks[prov.name] ?? "";
    const card = el("div", { className: "provider" });
    const isEnabled = block !== "" && extractYAMLValue(block, "enabled") !== "false";

    const headerItems: HTMLElement[] = [el("span", null, prov.label)];
    headerItems.push(cfgToggle(`cfg-prov-${prov.name}-enabled`, isEnabled));
    card.appendChild(el("div", { className: "provider-head" }, ...headerItems));

    const details = el("div", { className: "provider-body" });
    if (!isEnabled) {
      details.style.display = "none";
    }

    const priVal = extractYAMLValue(block, "priority");
    details.appendChild(
      cfgField(
        `cfg-prov-${prov.name}-priority`,
        "Priority",
        "number",
        priVal || "",
        "99",
        "Tiebreaker when subtitles have equal scores. Lower = more trusted.",
      ),
    );

    if (prov.settings) {
      const settingsRaw = extractYAMLBlock(block, "settings");
      for (const sf of prov.settings) {
        const fid = `cfg-prov-${prov.name}-s-${sf.key}`;
        let val = "";
        if (settingsRaw) {
          val = extractYAMLValue(settingsRaw, sf.key);
        }
        details.appendChild(renderField(fid, sf, val));
      }
    }

    card.appendChild(details);

    const toggle = card.querySelector(`#cfg-prov-${prov.name}-enabled`);
    if (toggle) {
      toggle.addEventListener("change", () => {
        details.style.display = toggle.checked ? "" : "none";
      });
    }
    sec.appendChild(card);
  }

  // Async: fetch provider health and show status badges.
  apiGet<ProviderHealthResponse>("/api/providers/timeout")
    .then((data: ProviderHealthResponse | null) => {
      if (!data?.providers) {
        return;
      }
      for (const [name, status] of Object.entries(data.providers)) {
        if (name === EMBEDDED_PROVIDER) {
          continue;
        }
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
    .catch(() => {});

  return sec;
}

// Render embedded subtitle settings as a standalone section with
// simple toggles (not a provider card).
export function renderEmbeddedSection(
  schema: ConfigSchema,
  sections: Record<string, string>,
): HTMLElement {
  const sec = el("div", { className: "cfg-section" });
  sec.appendChild(el("div", { className: "cfg-title" }, "Embedded Subtitles"));

  const raw = sections["providers"] ?? "";
  const blocks = parseProviderBlocks(raw);
  const embProv = schema.providers.find((p: ProviderSchema) => p.name === EMBEDDED_PROVIDER);
  if (!embProv?.settings) {
    return sec;
  }

  const block = blocks[EMBEDDED_PROVIDER] ?? "";
  const settingsRaw = extractYAMLBlock(block, "settings");

  for (const sf of embProv.settings) {
    const fid = `cfg-prov-embedded-s-${sf.key}`;
    let val = "";
    if (settingsRaw) {
      val = extractYAMLValue(settingsRaw, sf.key);
    }
    sec.appendChild(renderField(fid, sf, val));
  }
  return sec;
}

export function genProviders(schema: ConfigSchema): string {
  const lines: string[] = ["providers:"];
  for (const prov of schema.providers) {
    lines.push(`  ${prov.name}:`);
    if (!prov.always_enabled) {
      const enEl = document.getElementById(
        `cfg-prov-${prov.name}-enabled`,
      ) as HTMLInputElement | null;
      lines.push(`    enabled: ${enEl ? String(enEl.checked) : "false"}`);
      const priEl = document.getElementById(
        `cfg-prov-${prov.name}-priority`,
      ) as HTMLInputElement | null;
      if (priEl && priEl.value) {
        lines.push(`    priority: ${priEl.value}`);
      }
    }
    if (prov.settings && prov.settings.length > 0) {
      lines.push("    settings:");
      for (const sf of prov.settings) {
        const fEl = document.getElementById(
          `cfg-prov-${prov.name}-s-${sf.key}`,
        ) as HTMLInputElement | null;
        if (!fEl) {
          continue;
        }
        let val: string;
        if (fEl.type === "checkbox") {
          val = String(fEl.checked);
        } else if (sf.secret && !fEl.value) {
          val = '""';
        } else {
          val = fEl.value || '""';
        }
        lines.push(`      ${sf.key}: ${val}`);
      }
    }
  }
  return lines.join("\n");
}
