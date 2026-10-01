// A non-admin is routed to the notice because every wizard endpoint is admin-gated.

import { describe, it, expect } from "vitest";
import { postLoginDestination } from "./post-login.js";

describe("postLoginDestination", () => {
  it("routes an admin into the wizard while the config is invalid", () => {
    expect(postLoginDestination("admin", false)).toBe("wizard");
  });
  it("routes a non-admin to the finish-setup notice, never a wizard of 403s", () => {
    expect(postLoginDestination("user", false)).toBe("admin_needed_notice");
    expect(postLoginDestination("", false)).toBe("admin_needed_notice");
  });
  it("routes everyone to the app when the config is valid", () => {
    expect(postLoginDestination("admin", true)).toBe("app");
    expect(postLoginDestination("user", true)).toBe("app");
  });
});
