// nav-app.ts — the one navigation out of a login.html flow and into the app.
//
// Its own module because `window.location` cannot be substituted in a real
// browser: `window` and `location` are non-configurable, so an assignment
// reloads the test runner's own page and fails the file. Replacing a caller's
// import of THIS module is the only seam a test has.

/** Leaves login.html for the app shell. */
export function navigateToApp(): void {
  window.location.href = "/";
}
