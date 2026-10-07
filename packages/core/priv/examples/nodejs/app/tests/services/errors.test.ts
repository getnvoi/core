import { describe, expect, it } from "vitest";
import { userFacing } from "~/services/errors.server";

/**
 * WHAT A STRANGER IS ALLOWED TO BE TOLD.
 *
 * Both auth routes sent `error.message` to the browser, and drizzle's messages
 * embed the failing query and its parameters — so a constraint violation
 * rendered a bcrypt hash into the sign-up form.
 */
describe("userFacing", () => {
  it("does not leak a database error, or anything in it", () => {
    const drizzle = new Error(
      'Failed query: insert into "users" ("id", "email", "password_hash") ' +
        "values (?, ?, ?) params: b90a3d00,dogfood@nvoi.test,$2b$12$d7yUcGr2bDijdKkoqzwsm",
    );

    const { message, internal } = userFacing(drizzle);

    expect(message).toBe("Something went wrong. Please try again.");
    expect(message).not.toContain("$2b$");
    expect(message).not.toContain("insert into");
    // The real thing is still available to log — hiding it from the user must
    // not hide it from us.
    expect(internal).toContain("insert into");
  });

  it("keeps the messages a person can act on", () => {
    for (const intended of ["Email already registered", "Invalid email or password"]) {
      expect(userFacing(new Error(intended)).message).toBe(intended);
    }
  });

  it("keeps password validation, which is the whole point of showing an error", () => {
    const message = "Password must be at least 12 characters";
    expect(userFacing(new Error(message)).message).toBe(message);
  });

  it("handles something that is not an Error at all", () => {
    expect(userFacing("boom").message).toBe("Something went wrong. Please try again.");
    expect(userFacing(undefined).message).toBe("Something went wrong. Please try again.");
  });
});
