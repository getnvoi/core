import { afterEach, beforeAll } from "vitest";
import { cleanup } from "@testing-library/react";
import "@testing-library/jest-dom/vitest";

/**
 * A REAL DATABASE, and there is nowhere to conjure one.
 *
 * The SQLite twin makes a temporary file per run. Postgres is a server, so a
 * test needs one that exists — `DATABASE_URL`, the same variable the app reads,
 * pointed at a scratch database.
 *
 * NOT DEFAULTED. A test suite that invents `postgres://localhost/postgres`
 * silently runs against whatever is on the machine, which is somebody's real
 * data on a laptop and nothing at all in CI. Missing is a refusal.
 */
beforeAll(() => {
  if (!process.env.DATABASE_URL) {
    throw new Error(
      "DATABASE_URL is not set. These tests use a real Postgres database; " +
        "point it at a scratch one — the app reads the same variable.",
    );
  }
});

afterEach(() => {
  cleanup();
});
