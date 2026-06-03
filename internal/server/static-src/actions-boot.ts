// Wire @cplieger/actions to subflux's notification and API layers.
// Call initActions() once at app boot before any action is dispatched.

import { configure, configureApi } from "@cplieger/actions";
import { success, error } from "./notify.js";

export function initActions(): void {
  configure({
    success: (msg) => success(msg),
    error: (msg, retry) => error(msg, retry),
  });
  configureApi({
    credentials: "same-origin",
  });
}
