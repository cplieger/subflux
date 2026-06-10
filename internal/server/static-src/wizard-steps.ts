// wizard-steps.ts — Extracted wizard step builders (Arr, Media Roots, Search, Post-Processing).

import { apiPost } from "./api-client.js";
import { $ } from "./dom-core.js";
import { el } from "./dom.js";
import type { SchemaField, SchemaSection } from "./api-types.js";
import {
  type WizardStep,
  schemaByKey,
  wizardValues,
  mediaRoots,
  infoIcon,
  wizField,
  wizToggle,
} from "./wizard.js";

// --- Step 1: Sonarr + Radarr ---

export function buildArrStep(): WizardStep {
  return {
    id: "arr",
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
      const sf = (sv["url"] ?? "").trim() !== "" || (sv["api_key"] ?? "").trim() !== "";
      const rf = (rv["url"] ?? "").trim() !== "" || (rv["api_key"] ?? "").trim() !== "";
      if (!sf && !rf) {
        return "At least one of Sonarr or Radarr must be configured";
      }
      if (sf) {
        if (!(sv["url"] ?? "").trim()) {
          return "Sonarr URL is required";
        }
        if (!(sv["api_key"] ?? "").trim()) {
          return "Sonarr API Key is required";
        }
      }
      if (rf) {
        if (!(rv["url"] ?? "").trim()) {
          return "Radarr URL is required";
        }
        if (!(rv["api_key"] ?? "").trim()) {
          return "Radarr API Key is required";
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
    group.appendChild(
      wizField(
        "wiz-" + key + "-" + field.key,
        field.label,
        field.type === "secret" ? "secret" : "text",
        saved[field.key] ?? "",
        field.placeholder ?? "",
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
    id: "media_roots",
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
        const data = await apiPost<{ valid: boolean; error?: string }>(
          "/api/config/validate-path",
          { path: p.trim() },
          signal,
        );
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
    id: "search",
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

        const fieldsDiv = el("div", { id: "wiz-adaptive-fields" });
        if (!enabledChecked) {
          fieldsDiv.hidden = true;
        }

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

        cb.addEventListener("change", () => {
          fieldsDiv.hidden = !cb.checked;
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
        wizardValues["search"] = vals;
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
        wizardValues["adaptive"] = vals;
      }
    },
    validate(): string {
      return "";
    },
  };
}

// --- Step 6: Post-Processing ---

export function buildPostProcessStep(): WizardStep {
  return {
    id: "post_processing",
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
