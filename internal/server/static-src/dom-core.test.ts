import { describe, it, beforeEach, afterEach, expect } from "vitest";
import { $, show, showPage, showError, hideError } from "./dom-core.js";

// A real Chromium runs these (see vitest.config.ts), which is what makes the
// focus assertions meaningful: document.activeElement only moves for a field
// the browser actually renders, so a fixture that mirrors login.html's
// hidden-by-default page-states exercises the real behavior.

/** mountAuthPages builds a reduced login.html: four .auth-page states, all
 *  hidden, two of them credential forms with the same field names. */
function mountAuthPages(): void {
  document.body.innerHTML = `
    <main id="loginPage" class="auth-page" hidden>
      <div id="loginError" class="auth-error" hidden></div>
      <form id="loginForm">
        <label for="username">Username</label>
        <input type="text" id="username" name="username" autocomplete="username webauthn" required>
        <label for="password">Password</label>
        <input type="password" id="password" name="password" autocomplete="current-password" required>
        <button type="submit" id="loginBtn">Sign in</button>
      </form>
    </main>
    <main id="setupPage" class="auth-page" hidden>
      <form id="setupForm">
        <label for="setupUsername">Username</label>
        <input type="text" id="setupUsername" name="username" autocomplete="username" required>
        <label for="setupPassword">Password</label>
        <input type="password" id="setupPassword" name="password" autocomplete="new-password" required>
        <button type="submit">Create Account</button>
      </form>
    </main>
    <main id="setupNoticePage" class="auth-page" hidden>
      <p>Setup is not finished</p>
    </main>
    <main id="configWizardPage" class="auth-page" hidden>
      <div id="wizardSection"></div>
    </main>`;
}

describe("dom-core: showPage()", () => {
  beforeEach(() => {
    mountAuthPages();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("reveals only the named page and hides every sibling", () => {
    showPage("setupPage");

    expect($("setupPage")?.hidden).toBe(false);
    expect($("loginPage")?.hidden).toBe(true);
    expect($("setupNoticePage")?.hidden).toBe(true);
    expect($("configWizardPage")?.hidden).toBe(true);
  });

  it("hides a previously revealed page when another is shown", () => {
    showPage("loginPage");
    showPage("setupPage");

    expect($("loginPage")?.hidden).toBe(true);
    expect($("setupPage")?.hidden).toBe(false);
  });

  // The bug this covers: both username inputs used to carry `autofocus`, so
  // the attribute resolved once at parse against #username — inside the page
  // that is then hidden — and the setup card's username field never received
  // focus. An unfocused username field gets no password-manager fill
  // affordance, while its type=password sibling gets one unconditionally.
  it("focuses the revealed page's first field, not a hidden sibling page's", () => {
    showPage("setupPage");

    expect(document.activeElement?.id).toBe("setupUsername");
  });

  it("focuses the login username when the login page is the one revealed", () => {
    showPage("loginPage");

    expect(document.activeElement?.id).toBe("username");
  });

  it("skips a hidden field (the OIDC link-on-login shape)", () => {
    // wireOIDCLinkForm hides the username field itself: focus must fall
    // through to the password field rather than land on a field the browser
    // does not render.
    const user = $("username");
    if (user) {
      user.hidden = true;
    }

    showPage("loginPage");

    expect(document.activeElement?.id).toBe("password");
  });

  it("skips a field inside a hidden wrapper", () => {
    const user = $("username");
    const wrapper = document.createElement("div");
    wrapper.hidden = true;
    user?.replaceWith(wrapper);
    if (user) {
      wrapper.append(user);
    }

    showPage("loginPage");

    expect(document.activeElement?.id).toBe("password");
  });

  it("leaves focus alone when it already sits inside the revealed page", () => {
    showPage("loginPage");
    $("password")?.focus();

    showPage("loginPage");

    expect(document.activeElement?.id).toBe("password");
  });

  it("does not throw for an unknown page id", () => {
    expect(() => {
      showPage("noSuchPage");
    }).not.toThrow();
  });

  it("focuses nothing on a page with no form controls", () => {
    showPage("setupNoticePage");

    expect(document.activeElement).toBe(document.body);
  });
});

describe("dom-core: show(), showError(), hideError()", () => {
  beforeEach(() => {
    mountAuthPages();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("show() clears the hidden attribute", () => {
    show($("loginError"));

    expect($("loginError")?.hidden).toBe(false);
  });

  it("show() tolerates a null element", () => {
    expect(() => {
      show(null);
    }).not.toThrow();
  });

  it("showError() sets the text and reveals the element", () => {
    showError("loginError", "Invalid credentials");

    expect($("loginError")?.textContent).toBe("Invalid credentials");
    expect($("loginError")?.hidden).toBe(false);
  });

  it("showError() ignores an unknown id", () => {
    expect(() => {
      showError("noSuchError", "boom");
    }).not.toThrow();
  });

  it("hideError() re-hides the element", () => {
    showError("loginError", "Invalid credentials");
    hideError("loginError");

    expect($("loginError")?.hidden).toBe(true);
  });
});
