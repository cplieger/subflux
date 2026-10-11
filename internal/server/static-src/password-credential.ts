// password-credential.ts — Ask the browser to save a password the operator
// just chose, over the Credential Management API. Zero imports.

/** PasswordCredentialData is the constructor argument, narrowed to the fields
 *  this module supplies: `id` is the username the credential is keyed by.
 *  https://w3c.github.io/webappsec-credential-management/#passwordcredential */
interface PasswordCredentialData {
  id: string;
  password: string;
}

/** PasswordCredential is Chromium-only and absent from TypeScript's DOM lib,
 *  so only the sliver constructed here is declared. Its instances are plain
 *  Credentials, which is what navigator.credentials.store accepts. */
type PasswordCredentialCtor = new (data: PasswordCredentialData) => Credential;

/** storePasswordCredential asks the browser's password manager to save
 *  `username`/`password`. It resolves either way and never rejects.
 *
 *  Why an explicit request is needed: the admin-creation form submits over
 *  fetch and hands off to the config wizard through history.replaceState, so
 *  NO navigation follows the credential the operator just typed. That
 *  navigation is what Chromium's own save-password heuristic waits for, and
 *  the wizard's closing navigation to "/" arrives minutes later with the form
 *  long gone from the DOM. Without this call the first admin's password is
 *  never offered for saving.
 *
 *  It ADDS a prompt where one is missing rather than replacing one that works.
 *  Safari and Firefox never shipped PasswordCredential, and third-party
 *  managers hook the form-submit path instead of this API, so both keep
 *  whatever they already do; absent support this does nothing.
 *
 *  Never throws, and callers need not await it: no prompt is a lost
 *  convenience, while the caller is mid-flow into the wizard. */
export async function storePasswordCredential(username: string, password: string): Promise<void> {
  if (!username || !password) {
    return;
  }
  const ctor = (window as unknown as { PasswordCredential?: PasswordCredentialCtor })
    .PasswordCredential;
  if (typeof ctor !== "function") {
    return;
  }
  try {
    await navigator.credentials.store(new ctor({ id: username, password }));
  } catch {
    /* A declined, blocked or unimplemented store is not a setup failure. */
  }
}
