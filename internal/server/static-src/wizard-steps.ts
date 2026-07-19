// wizard-steps.ts — Extracted wizard step builders (Arr, Media Roots,
// Search, Scoring, Post-Processing). Steps render prefilled from the
// structured config (module bindings adopted by wizard.ts at boot); saved
// secrets render a keep-placeholder instead of a value.

import { validateConfigPath } from "./wire/client.gen.js";
import { $ } from "./dom-core.js";
import { el } from "./dom.js";
import { createDisclosure } from "@cplieger/ui-primitives/disclosure";
import type { SchemaField, SchemaSection } from "./api-types.js";
import {
  type WizardStep,
  schemaByKey,
  secretSaved,
  wizardValues,
  mediaRoots,
  infoIcon,
  wizField,
  wizToggle,
} from "./wizard.js";

/** SECRET_SAVED_PLACEHOLDER marks a secret the config file already holds:
 *  leaving the field blank keeps the stored value (server-side merge). */
export const SECRET_SAVED_PLACEHOLDER = "\u2022\u2022\u2022\u2022 saved \u2014 leave blank to keep";

// --- Step 1: Sonarr + Radarr ---

export function buildArrStep(): WizardStep {
  return {
    stepId: "arr",
    title: "Sonarr & Radarr",
    render(container: HTMLElement): void {
      const sonarr = schemaByKey("sonarr");
      const radarr = schemaByKey("radarr");
      if (sonarr) {
        renderArrGroup(container, "sonarr", "Sonarr", sonarr);
      }
      if (radarr) {
        renderArrGroup(container, "radarr", "Radarr", radarr);
      }
    },
    collect(): void {
      collectArrValues("sonarr");
      collectArrValues("radarr");
    },
    validate(): string {
      const sv = wizardValues["sonarr"] ?? {};
      const rv = wizardValues["radarr"] ?? {};
      // A saved API key (presence flag) satisfies the key requirement: the
      // redacted-empty field means "keep the stored secret".
      const sKey = (sv["api_key"] ?? "").trim() !== "" || secretSaved("sonarr.api_key");
      const rKey = (rv["api_key"] ?? "").trim() !== "" || secretSaved("radarr.api_key");
      const sf = (sv["url"] ?? "").trim() !== "" || (sv["api_key"] ?? "").trim() !== "";
      const rf = (rv["url"] ?? "").trim() !== "" || (rv["api_key"] ?? "").trim() !== "";
      if (!sf && !rf) {
        return "At least one of Sonarr or Radarr must be configured";
      }
      if (sf) {
        if (!(sv["url"] ?? "").trim()) {
          return "Sonarr URL is required";
        }
        if (!sKey) {
          return "Sonarr API Key is required (or clear the Sonarr URL if you don't use Sonarr)";
        }
      }
      if (rf) {
        if (!(rv["url"] ?? "").trim()) {
          return "Radarr URL is required";
        }
        if (!rKey) {
          return "Radarr API Key is required (or clear the Radarr URL if you don't use Radarr)";
        }
      }
      return "";
    },
  };
}

function renderArrGroup(
  container: HTMLElement,
  key: string,
  name: string,
  section: SchemaSection,
): void {
  const header = el("div", { className: "wiz-arr-header" }, el("span", null, name));
  const group = el("div", { className: "wiz-arr-group" }, header);
  const saved = wizardValues[key] ?? {};
  for (const field of section.fields ?? []) {
    const isSavedSecret = field.type === "secret" && secretSaved(key + "." + field.key);
    group.appendChild(
      wizField(
        "wiz-" + key + "-" + field.key,
        field.label,
        field.type === "secret" ? "secret" : "text",
        saved[field.key] ?? "",
        isSavedSecret ? SECRET_SAVED_PLACEHOLDER : (field.placeholder ?? ""),
        field.help,
      ),
    );
  }
  container.appendChild(group);
}

function collectArrValues(key: string): void {
  const section = schemaByKey(key);
  if (!section?.fields) {
    return;
  }
  const values: Record<string, string> = {};
  for (const field of section.fields ?? []) {
    const inp = $("wiz-" + key + "-" + field.key) as HTMLInputElement | null;
    if (inp) {
      values[field.key] = inp.value;
    }
  }
  wizardValues[key] = values;
}

// --- Step 2: Media Roots ---

export function buildMediaRootsStep(): WizardStep {
  return {
    stepId: "media_roots",
    title: "Media Roots",
    render(container: HTMLElement): void {
      container.appendChild(
        el(
          "p",
          {
            style: "font-size:var(--text-sm);color:var(--text-2);margin-block-end:var(--sp-4)",
          },
          "Paths to your media folders as seen inside the container (must match Sonarr/Radarr root folders).",
        ),
      );

      const listContainer = el("div", { id: "wiz-media-roots" });
      container.appendChild(listContainer);
      if (mediaRoots.length === 0) {
        mediaRoots.push("");
      }
      renderMediaRoots(listContainer);

      container.appendChild(
        el(
          "button",
          {
            type: "button",
            className: "wiz-lang-add",
            onclick: () => {
              collectMediaRoots();
              mediaRoots.push("");
              renderMediaRoots(listContainer);
            },
          },
          "+ Add path",
        ),
      );
    },
    collect(): void {
      collectMediaRoots();
    },
    validate(): string {
      collectMediaRoots();
      const valid = mediaRoots.filter((p: string) => p.trim() !== "");
      if (valid.length === 0) {
        return "At least one media root path is required";
      }
      for (const p of valid) {
        if (!p.startsWith("/")) {
          return "Media root paths must be absolute (start with /): " + p;
        }
      }
      return "";
    },
    async validateAsync(signal: AbortSignal): Promise<string> {
      collectMediaRoots();
      const valid = mediaRoots.filter((p: string) => p.trim() !== "");
      for (const p of valid) {
        if (signal.aborted) {
          return "";
        }
        const data = await validateConfigPath({ path: p.trim() }, { signal });
        // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
        if (signal.aborted) {
          return "";
        }
        if (!data) {
          return p + ": request failed";
        }
        if (!data.valid) {
          return p + ": " + (data.error ?? "path does not exist");
        }
      }
      return "";
    },
  };
}

function renderMediaRoots(container: HTMLElement): void {
  container.replaceChildren();
  for (let i = 0; i < mediaRoots.length; i++) {
    const row = el("div", { className: "wiz-lang-row" });
    row.appendChild(
      el("input", {
        type: "text",
        id: "wiz-media-root-" + String(i),
        className: "wiz-media-input",
        placeholder: "/media",
        value: mediaRoots[i] ?? "",
        autocomplete: "off",
        "data-1p-ignore": "",
        "data-lpignore": "true",
        "data-bwignore": "",
        "data-form-type": "other",
      }),
    );
    if (mediaRoots.length > 1) {
      const idx = i;
      const rm = el(
        "button",
        {
          type: "button",
          className: "wiz-lang-remove",
          onclick: () => {
            collectMediaRoots();
            mediaRoots.splice(idx, 1);
            renderMediaRoots(container);
          },
        },
        "\u00d7",
      );
      row.appendChild(rm);
    }
    container.appendChild(row);
  }
}

function collectMediaRoots(): void {
  for (let i = 0; i < mediaRoots.length; i++) {
    const inp = $("wiz-media-root-" + String(i)) as HTMLInputElement | null;
    if (inp) {
      mediaRoots[i] = inp.value;
    }
  }
}

// --- Step 5: Search & Adaptive ---

export function buildSearchStep(): WizardStep {
  return {
    stepId: "search",
    title: "Search Settings",
    render(container: HTMLElement): void {
      const search = schemaByKey("search");
      const adaptive = schemaByKey("adaptive");
      const savedSearch = wizardValues["search"] ?? {};
      const savedAdaptive = wizardValues["adaptive"] ?? {};

      if (search?.fields) {
        const allowed = [
          "scan_interval",
          "exclude_arr_tags",
          "upgrade_enabled",
          "upgrade_window_days",
        ];
        for (const f of search.fields) {
          if (!allowed.includes(f.key)) {
            continue;
          }
          const sv = savedSearch[f.key] ?? f.default ?? "";
          if (f.type === "bool") {
            const checked = sv === "true";
            container.appendChild(wizToggle("wiz-search-" + f.key, f.label, checked, f.help));
            if (f.key === "upgrade_enabled") {
              const windowField = search.fields.find(
                (x: SchemaField) => x.key === "upgrade_window_days",
              );
              if (windowField) {
                const wv = savedSearch["upgrade_window_days"] ?? windowField.default ?? "";
                const windowEl = wizField(
                  "wiz-search-upgrade_window_days",
                  windowField.label,
                  windowField.type || "number",
                  wv,
                  windowField.placeholder ?? windowField.default ?? "",
                  windowField.help,
                );
                if (!checked) {
                  windowEl.hidden = true;
                }
                windowEl.id = "wiz-search-upgrade-window-row";
                container.appendChild(windowEl);
                const cb = $("wiz-search-upgrade_enabled") as HTMLInputElement | null;
                if (cb) {
                  cb.addEventListener("change", () => {
                    windowEl.hidden = !cb.checked;
                  });
                }
              }
            }
          } else if (f.key === "upgrade_window_days") {
            continue;
          } else {
            container.appendChild(
              wizField(
                "wiz-search-" + f.key,
                f.label,
                f.type || "text",
                sv,
                f.placeholder ?? f.default ?? "",
                f.help,
              ),
            );
          }
        }
      }

      if (adaptive?.fields) {
        const titleSpan = el("span", null, "Adaptive Backoff");
        titleSpan.appendChild(
          infoIcon("Gradually increases delay between retries when no subtitles are found"),
        );

        const enabledVal = savedAdaptive["enabled"];
        const enabledChecked = enabledVal !== undefined ? enabledVal === "true" : true;
        const cb = el("input", {
          type: "checkbox",
          id: "wiz-adaptive-enabled",
        }) as HTMLInputElement;
        cb.checked = enabledChecked;
        const toggle = el("label", { className: "wiz-toggle" }, cb, el("span"));

        const headerRow = el("div", { className: "wiz-adaptive-header" }, titleSpan, toggle);
        container.appendChild(headerRow);

        // Region-only disclosure: the header checkbox is the visible control,
        // the primitive drives the animated height + aria-hidden/inert.
        const fieldsDiv = el("div", { id: "wiz-adaptive-fields" });

        for (const f of adaptive.fields) {
          if (f.type === "bool") {
            continue;
          }
          const sv = savedAdaptive[f.key] ?? f.default ?? "";
          fieldsDiv.appendChild(
            wizField(
              "wiz-adaptive-" + f.key,
              f.label,
              f.type || "text",
              sv,
              f.placeholder ?? f.default ?? "",
              f.help,
            ),
          );
        }
        container.appendChild(fieldsDiv);
        const fieldsCtl = createDisclosure(null, fieldsDiv, { open: enabledChecked });

        cb.addEventListener("change", () => {
          if (cb.checked) {
            fieldsCtl.open();
          } else {
            fieldsCtl.close();
          }
        });
      }
    },
    collect(): void {
      const search = schemaByKey("search");
      if (search?.fields) {
        const vals: Record<string, string> = {};
        const allowed = [
          "scan_interval",
          "exclude_arr_tags",
          "upgrade_enabled",
          "upgrade_window_days",
        ];
        for (const f of search.fields) {
          if (!allowed.includes(f.key)) {
            continue;
          }
          const inp = $("wiz-search-" + f.key);
          if (!inp) {
            continue;
          }
          if (f.type === "bool") {
            vals[f.key] = String((inp as HTMLInputElement).checked);
          } else {
            vals[f.key] = (inp as HTMLInputElement).value;
          }
        }
        // Preserve prefilled values for fields the wizard never renders
        // (provider_timeout, scan_delay, ...): the save path merges over the
        // boot section anyway, but the model should not lose them either.
        wizardValues["search"] = { ...(wizardValues["search"] ?? {}), ...vals };
      }
      const adaptive = schemaByKey("adaptive");
      if (adaptive?.fields) {
        const vals: Record<string, string> = {};
        const cb = $("wiz-adaptive-enabled") as HTMLInputElement | null;
        if (cb) {
          vals["enabled"] = String(cb.checked);
        }
        for (const f of adaptive.fields) {
          if (f.type === "bool") {
            continue;
          }
          const inp = $("wiz-adaptive-" + f.key) as HTMLInputElement | null;
          if (inp) {
            vals[f.key] = inp.value;
          }
        }
        wizardValues["adaptive"] = { ...(wizardValues["adaptive"] ?? {}), ...vals };
      }
    },
    validate(): string {
      return "";
    },
  };
}

// --- Step 6: Scoring ---

export function buildScoringStep(): WizardStep {
  return {
    stepId: "scoring",
    title: "Scoring",
    render(container: HTMLElement): void {
      const section = schemaByKey("scoring");
      if (!section?.fields) {
        return;
      }
      container.appendChild(
        el(
          "p",
          {
            style: "font-size:var(--text-sm);color:var(--text-2);margin-block-end:var(--sp-4)",
          },
          "Weights control how subtitle releases are ranked against your video. The defaults work well; tune only if you know your library.",
        ),
      );
      const saved = wizardValues["scoring"] ?? {};
      for (const field of section.fields ?? []) {
        container.appendChild(
          wizField(
            "wiz-scoring-" + field.key,
            field.label,
            "number",
            saved[field.key] ?? "",
            field.placeholder ?? field.default ?? "",
            field.help,
          ),
        );
      }
    },
    collect(): void {
      const section = schemaByKey("scoring");
      if (!section?.fields) {
        return;
      }
      const values: Record<string, string> = {};
      for (const field of section.fields ?? []) {
        const inp = $("wiz-scoring-" + field.key) as HTMLInputElement | null;
        if (inp) {
          values[field.key] = inp.value;
        }
      }
      wizardValues["scoring"] = values;
    },
    validate(): string {
      const vals = wizardValues["scoring"] ?? {};
      for (const [k, v] of Object.entries(vals)) {
        if (v.trim() !== "" && !/^\d+$/.test(v.trim())) {
          return "Scoring weight " + k + " must be a whole number";
        }
      }
      return "";
    },
  };
}

// --- Step 7: Post-Processing ---

export function buildPostProcessStep(): WizardStep {
  return {
    stepId: "post_processing",
    title: "Post-Processing",
    render(container: HTMLElement): void {
      const section = schemaByKey("post_processing");
      if (!section?.fields) {
        return;
      }
      const saved = wizardValues["post_processing"] ?? {};
      for (const field of section.fields ?? []) {
        const savedVal = saved[field.key];
        if (field.type === "bool") {
          const checked = savedVal !== undefined ? savedVal === "true" : field.default === "true";
          const row = wizToggle("wiz-pp-" + field.key, field.label, checked, field.help);
          container.appendChild(row);
        } else {
          container.appendChild(
            wizField(
              "wiz-pp-" + field.key,
              field.label,
              field.type || "text",
              savedVal ?? "",
              field.placeholder ?? field.default ?? "",
              field.help,
            ),
          );
        }
      }

      const syncCb = $("wiz-pp-sync_subtitles") as HTMLInputElement | null;
      const audioCb = $("wiz-pp-audio_sync_fallback") as HTMLInputElement | null;
      if (syncCb && audioCb) {
        const updateAudio = (): void => {
          audioCb.disabled = !syncCb.checked;
          if (!syncCb.checked) {
            audioCb.checked = false;
          }
        };
        updateAudio();
        syncCb.addEventListener("change", updateAudio);
      }
    },
    collect(): void {
      const section = schemaByKey("post_processing");
      if (!section?.fields) {
        return;
      }
      const values: Record<string, string> = {};
      for (const field of section.fields ?? []) {
        const inp = $("wiz-pp-" + field.key);
        if (!inp) {
          continue;
        }
        if (field.type === "bool") {
          values[field.key] = String((inp as HTMLInputElement).checked);
        } else {
          values[field.key] = (inp as HTMLInputElement).value;
        }
      }
      wizardValues["post_processing"] = values;
    },
    validate(): string {
      return "";
    },
  };
}
