// wizard-providers.ts — Extracted wizard providers step.

import { $ } from "./dom-core.js";
import { el } from "./dom.js";
import type { ProviderSchema, SchemaField } from "./api-types.js";
import {
  type WizardStep,
  schemaByKey,
  wizardValues,
  providerEnabled,
  wizField,
  wizToggle,
} from "./wizard.js";

export function buildProvidersStep(): WizardStep {
  return {
    id: "providers",
    title: "Providers",
    render(container: HTMLElement): void {
      const section = schemaByKey("providers");
      if (!section?.providers) {
        return;
      }
      const embedded = section.providers.find((p: ProviderSchema) => p.always_enabled);
      if (embedded) {
        renderProviderCard(container, embedded, true);
      }
      for (const prov of section.providers) {
        if (prov.always_enabled) {
          continue;
        }
        renderProviderCard(container, prov, false);
      }
    },
    collect(): void {
      collectProviders();
    },
    validate(): string {
      collectProviders();
      const section = schemaByKey("providers");
      if (!section?.providers) {
        return "";
      }
      for (const prov of section.providers) {
        if (!providerEnabled[prov.name]) {
          continue;
        }
        if (prov.always_enabled) {
          continue;
        }
        const settings = prov.settings;
        if (!settings) {
          continue;
        }
        const credFields = settings.filter((f: SchemaField) => f.type !== "bool");
        if (credFields.length === 0) {
          continue;
        }
        const hasAnyCred = credFields.some((f: SchemaField) => {
          const inp = $("wiz-prov-" + prov.name + "-" + f.key) as HTMLInputElement | null;
          return inp !== null && inp.value.trim() !== "";
        });
        if (!hasAnyCred) {
          return (
            prov.label +
            " is enabled but has no credentials configured. " +
            "Disable it or fill in the required fields."
          );
        }
      }
      return "";
    },
  };
}

function renderProviderCard(container: HTMLElement, prov: ProviderSchema, isAlways: boolean): void {
  const enabled = isAlways || providerEnabled[prov.name] === true;
  const card = el("div", { className: "wiz-prov-card" + (enabled ? " open" : "") });

  const header = el("div", { className: "wiz-prov-header" });
  header.appendChild(el("span", { className: "wiz-prov-name" }, prov.label));

  if (!isAlways) {
    const cb = el("input", {
      type: "checkbox",
      id: "wiz-prov-" + prov.name,
    }) as HTMLInputElement;
    cb.checked = enabled;
    const toggle = el("label", { className: "wiz-toggle" }, cb, el("span"));
    header.appendChild(toggle);
    cb.addEventListener("change", () => {
      card.classList.toggle("open", cb.checked);
      const body = card.querySelector<HTMLElement>(".wiz-prov-body");
      if (body) {
        body.hidden = !cb.checked;
      }
    });
  }
  card.appendChild(header);

  const settings = prov.settings;
  if (settings && settings.length > 0) {
    const body = el("div", { className: "wiz-prov-body" });
    if (!enabled) {
      body.hidden = true;
    }
    const savedProv = wizardValues["prov_" + prov.name] ?? {};
    for (const f of settings) {
      const savedVal = savedProv[f.key] ?? "";
      if (f.type === "bool") {
        const checked = savedVal !== "" ? savedVal === "true" : f.default === "true";
        body.appendChild(
          wizToggle("wiz-prov-" + prov.name + "-" + f.key, f.label, checked, f.help),
        );
      } else {
        body.appendChild(
          wizField(
            "wiz-prov-" + prov.name + "-" + f.key,
            f.label,
            f.secret ? "secret" : f.type || "text",
            savedVal,
            f.placeholder ?? f.default ?? "",
            f.help,
          ),
        );
      }
    }
    card.appendChild(body);
  }
  container.appendChild(card);
}

function collectProviders(): void {
  const section = schemaByKey("providers");
  if (!section?.providers) {
    return;
  }
  for (const prov of section.providers) {
    if (prov.always_enabled) {
      providerEnabled[prov.name] = true;
    } else {
      const cb = $("wiz-prov-" + prov.name) as HTMLInputElement | null;
      if (cb) {
        providerEnabled[prov.name] = cb.checked;
      }
    }
    const settings = prov.settings;
    if (settings && settings.length > 0) {
      const vals: Record<string, string> = {};
      for (const f of settings) {
        const inp = $("wiz-prov-" + prov.name + "-" + f.key);
        if (!inp) {
          continue;
        }
        if (f.type === "bool") {
          vals[f.key] = String((inp as HTMLInputElement).checked);
        } else {
          vals[f.key] = (inp as HTMLInputElement).value;
        }
      }
      wizardValues["prov_" + prov.name] = vals;
    }
  }
}
