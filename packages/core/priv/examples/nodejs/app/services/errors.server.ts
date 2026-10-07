/**
 * What a person is allowed to be told when something fails.
 *
 * ## Why this exists
 *
 * Both auth routes did `error instanceof Error ? error.message : "…"` and sent
 * it to the browser. Drizzle's messages embed the failing query AND its
 * parameters, so a constraint violation rendered a bcrypt hash into the page:
 *
 *     Failed query: insert into "users" (…) params: …,$2b$12$d7yUcGr2bDijd…
 *
 * ## An allowlist, not a filter
 *
 * The messages a service raises ON PURPOSE are listed here. Everything else is
 * an accident — a constraint, a driver, a bug — and an accident's text is
 * written for us, not for whoever is trying to sign in.
 *
 * A deny-list of "things that look secret" is the wrong shape: it has to be
 * edited every time a driver changes its wording, and forgetting shows a
 * password hash to a stranger.
 */
const INTENDED = new Set([
  "Email already registered",
  "Invalid email or password",
  "Email and password are required",
]);

/**
 * The message to show, and the one to log.
 *
 * The caller logs `internal` itself: this module has no business deciding where
 * a log goes, and swallowing it here is how a failure becomes invisible.
 */
export function userFacing(error: unknown): { message: string; internal: string } {
  const internal = error instanceof Error ? error.stack || error.message : String(error);

  if (error instanceof Error) {
    // Validation messages are composed at the point of failure and are the
    // whole reason a person can fix their input — "at least 12 characters" is
    // not something to replace with "something went wrong".
    if (INTENDED.has(error.message) || error.message.startsWith("Password must")) {
      return { message: error.message, internal };
    }
  }

  return { message: "Something went wrong. Please try again.", internal };
}
