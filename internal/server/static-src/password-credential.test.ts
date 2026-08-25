import { describe, it, beforeEach, afterEach, expect, vi } from "vitest";
import { storePasswordCredential } from "./password-credential.js";

// Every test states the PasswordCredential capability it assumes AT THE SITE
// rather than relying on what this Chromium build happens to implement, and
// navigator.credentials is replaced outright so no test can reach the real
// password manager. `unstubGlobals` in vitest.config undoes each stub; the
// navigator property is restored in afterEach because it is not a global.

interface StoredData {
  id: string;
  password: string;
}

const stored: StoredData[] = [];
let realCredentials: PropertyDescriptor | undefined;
let storeRejects: unknown = null;

/** installPasswordCredential puts a recording PasswordCredential constructor
 *  and a recording navigator.credentials.store in place. */
function installPasswordCredential(): void {
  vi.stubGlobal(
    "PasswordCredential",
    class {
      constructor(public data: StoredData) {}
    },
  );
  Object.defineProperty(navigator, "credentials", {
    configurable: true,
    value: {
      store: (cred: unknown) => {
        if (storeRejects) {
          return Promise.reject(storeRejects);
        }
        stored.push((cred as { data: StoredData }).data);
        return Promise.resolve();
      },
    },
  });
}

describe("password-credential: storePasswordCredential()", () => {
  beforeEach(() => {
    stored.length = 0;
    storeRejects = null;
    realCredentials = Object.getOwnPropertyDescriptor(navigator, "credentials");
  });

  afterEach(() => {
    if (realCredentials) {
      Object.defineProperty(navigator, "credentials", realCredentials);
    }
  });

  it("stores the username as the credential id and the password beside it", async () => {
    installPasswordCredential();

    await storePasswordCredential("admin", "a-long-enough-passphrase");

    expect(stored).toEqual([{ id: "admin", password: "a-long-enough-passphrase" }]);
  });

  it("does nothing when the browser has no PasswordCredential", async () => {
    installPasswordCredential();
    vi.stubGlobal("PasswordCredential", undefined);

    await storePasswordCredential("admin", "a-long-enough-passphrase");

    expect(stored).toEqual([]);
  });

  // Safari and Firefox: the name resolves to something that is not a
  // constructor rather than being absent outright.
  it("does nothing when PasswordCredential is not constructible", async () => {
    installPasswordCredential();
    vi.stubGlobal("PasswordCredential", {});

    await storePasswordCredential("admin", "a-long-enough-passphrase");

    expect(stored).toEqual([]);
  });

  it("resolves rather than rejecting when the store is refused", async () => {
    installPasswordCredential();
    storeRejects = new DOMException("blocked by permissions policy", "NotAllowedError");

    await expect(
      storePasswordCredential("admin", "a-long-enough-passphrase"),
    ).resolves.toBeUndefined();
  });

  it("skips an empty username", async () => {
    installPasswordCredential();

    await storePasswordCredential("", "a-long-enough-passphrase");

    expect(stored).toEqual([]);
  });

  it("skips an empty password", async () => {
    installPasswordCredential();

    await storePasswordCredential("admin", "");

    expect(stored).toEqual([]);
  });
});
