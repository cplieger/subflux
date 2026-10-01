// post-login.ts — Where a successful login lands. A leaf that imports nothing,
// so the login page can route a session without pulling in the wizard's chunk.

/** PostLoginDestination is where a successful login lands. */
export type PostLoginDestination = "app" | "wizard" | "admin_needed_notice";

/** postLoginDestination routes a fresh login: a valid config goes to the
 *  app; an invalid one sends admins into the wizard and non-admins to the
 *  "an admin needs to finish setup" notice (every wizard endpoint is
 *  admin-gated — walking a non-admin into 403s is not a flow). */
export function postLoginDestination(role: string, configValid: boolean): PostLoginDestination {
  if (configValid) {
    return "app";
  }
  return role === "admin" ? "wizard" : "admin_needed_notice";
}
