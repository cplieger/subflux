// @vitest-environment happy-dom
import { describe, it, vi, beforeEach } from "vitest";

vi.mock("./api-client.js", () => ({
  apiGet: vi.fn().mockResolvedValue(null),
  apiPostRaw: vi.fn().mockResolvedValue({ ok: true, data: null }),
}));
vi.mock("./actions/index.js", () => ({
  apiAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue({ ok: true }) })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn() }));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => {
    /* noop */
  }),
  emit: vi.fn(),
  BusEvent: { OpenSecurity: "open:security" },
}));

describe("security: passkeys section", () => {
  beforeEach(() => {
    document.body.innerHTML =
      '<dialog id="securityDialog"></dialog><dialog id="confirmDialog"></dialog>';
  });

  it.todo("renders 'No passkeys registered' when list is empty");

  it.todo("renders one row per passkey keyed by pk.id");

  it.todo("passkey row shows name and created date");

  it.todo("delete button removes passkey row via reconcile");

  it.todo("add passkey button triggers registration flow");

  it.todo("rename click makes name editable");
});

describe("security: API keys section", () => {
  it.todo("renders 'No API keys' when list is empty");

  it.todo("renders one row per key keyed by key.id");

  it.todo("generate button creates new key and shows it");

  it.todo("revoke button removes key row");
});

describe("security: password section", () => {
  it.todo("change password form validates matching passwords");

  it.todo("change password submits to API");
});
