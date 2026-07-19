// wizard-providers.ts — Extracted wizard providers step.

import { $ } from "./dom-core.js";
import { el } from "./dom.js";
import { createDisclosure, type DisclosureController } from "@cplieger/ui-primitives/disclosure";
import type { ProviderSchema, SchemaField } from "./api-types.js";
import {
  type WizardStep,
  bootSections,
  schemaByKey,
  secretSaved,
  wizardValues,
  providerEnabled,
  wizField,
  wizToggle,
} from "./wizard.js";
import { providerEnabledInSections } from "./wizard-state.js";
import { SECRET_SAVED_PLACEHOLDER } from "./wizard-steps.js";

/** provSecretPath is the dotted presence path of one provider setting. */
function provSecretPath(provName: string, fieldKey: string): string {
  return "providers." + provName + ".settings." + fieldKey;
}

export function buildProvidersStep(): WizardStep {
  return {
    stepId: "providers",
    title: "Providers",
    render(container: HTMLElement): void {
      const section = schemaByKey("providers");
      if (!section?.providers) {
        return;
      }
      for (const prov of section.providers) {
        renderProviderCard(container, prov);
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
        // A provider the (server-validated) config file already enables is
        // config-blessed: its credentials are optional by proof of a working
        // boot (the example config ships credential-optional providers like
        // animetosho enabled), so the wizard must not second-guess it.
        if (providerEnabledInSections(bootSections(), prov.name)) {
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
        // A credential counts as present when the field holds a value OR the
        // config file already stores that secret (presence flag).
        const hasAnyCred = credFields.some((f: SchemaField) => {
          const inp = $("wiz-prov-" + prov.name + "-" + f.key) as HTMLInputElement | null;
          if (inp !== null && inp.value.trim() !== "") {
            return true;
          }
          return f.secret === true && secretSaved(provSecretPath(prov.name, f.key));
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

function renderProviderCard(container: HTMLElement, prov: ProviderSchema): void {
  const enabled = providerEnabled[prov.name] === true;
  const card = el("div", { className: "wiz-prov-card" + (enabled ? " open" : "") });

  const header = el("div", { className: "wiz-prov-header" });
  header.appendChild(el("span", { className: "wiz-prov-name" }, prov.label));

  // Settings-body collapse controller — region-only disclosure (trigger:
  // null): the header checkbox is the visible control, the primitive drives
  // the animated height + aria-hidden/inert. Declared before the checkbox so
  // its change handler can drive it; stays null for settings-less providers.
  let bodyCtl: DisclosureController | null = null;

  const cb = el("input", {
    type: "checkbox",
    id: "wiz-prov-" + prov.name,
  }) as HTMLInputElement;
  cb.checked = enabled;
  const toggle = el("label", { className: "wiz-toggle" }, cb, el("span"));
  header.appendChild(toggle);
  cb.addEventListener("change", () => {
    card.classList.toggle("open", cb.checked);
    if (cb.checked) {
      bodyCtl?.open();
    } else {
      bodyCtl?.close();
    }
  });
  card.appendChild(header);

  const settings = prov.settings;
  if (settings && settings.length > 0) {
    // The region collapses to true zero height, so the padding + separator
    // live on an inner wrapper (the region itself carries no vertical chrome).
    const body = el("div", { className: "wiz-prov-body" });
    const inner = el("div", { className: "wiz-prov-inner" });
    body.appendChild(inner);
    const savedProv = wizardValues["prov_" + prov.name] ?? {};
    for (const f of settings) {
      const savedVal = savedProv[f.key] ?? "";
      if (f.type === "bool") {
        const checked = savedVal !== "" ? savedVal === "true" : f.default === "true";
        inner.appendChild(
          wizToggle("wiz-prov-" + prov.name + "-" + f.key, f.label, checked, f.help),
        );
      } else {
        const isSavedSecret = f.secret === true && secretSaved(provSecretPath(prov.name, f.key));
        inner.appendChild(
          wizField(
            "wiz-prov-" + prov.name + "-" + f.key,
            f.label,
            f.secret ? "secret" : f.type || "text",
            savedVal,
            isSavedSecret ? SECRET_SAVED_PLACEHOLDER : (f.placeholder ?? f.default ?? ""),
            f.help,
          ),
        );
      }
    }
    card.appendChild(body);
    bodyCtl = createDisclosure(null, body, { open: enabled });
  }
  container.appendChild(card);
}

function collectProviders(): void {
  const section = schemaByKey("providers");
  if (!section?.providers) {
    return;
  }
  for (const prov of section.providers) {
    const cb = $("wiz-prov-" + prov.name) as HTMLInputElement | null;
    if (cb) {
      providerEnabled[prov.name] = cb.checked;
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
