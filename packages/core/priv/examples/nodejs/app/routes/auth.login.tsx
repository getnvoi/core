import { data } from "react-router";
import type { Route } from "./+types/auth.login";
import { getDb } from "~/db";
import { userFacing } from "~/services/errors.server";
import { loginWithEmail } from "~/services/auth-email.server";
import { createUserSession } from "~/services/auth.server";
import { Field, Button } from "~/components";

export async function action({ request, context }: Route.ActionArgs) {
  const formData = await request.formData();
  const email = formData.get("email")?.toString();
  const password = formData.get("password")?.toString();

  if (!email || !password) {
    return data({ error: "Email and password are required" }, { status: 400 });
  }

  try {
    const db = getDb();
    const user = await loginWithEmail(db, email, password);
    return createUserSession(user, "/app");
  } catch (error) {
    const { message, internal } = userFacing(error);
    console.error("[login]", internal);
    return data({ error: message }, { status: 400 });
  }
}

export default function Login({ actionData }: Route.ComponentProps) {
  return (
    <div style={{ maxWidth: "400px", margin: "4rem auto", padding: "2rem" }}>
      <h1 style={{ marginBottom: "2rem" }}>Login</h1>

      <form method="post">
        <Field.Root name="email">
          <Field.Label>Email</Field.Label>
          <Field.Control type="email" required placeholder="you@example.com" />
        </Field.Root>

        <Field.Root name="password">
          <Field.Label>Password</Field.Label>
          <Field.Control type="password" required />
        </Field.Root>

        {actionData?.error && (
          <div style={{ color: "var(--color-red-600)", marginTop: "1rem" }}>
            {actionData.error}
          </div>
        )}

        <Button type="submit" style={{ marginTop: "1.5rem", width: "100%" }}>
          Login
        </Button>
      </form>

      <p style={{ marginTop: "1.5rem", textAlign: "center" }}>
        Don't have an account?{" "}
        <a href="/auth/register" style={{ color: "var(--color-blue-600)" }}>
          Sign up
        </a>
      </p>
    </div>
  );
}
