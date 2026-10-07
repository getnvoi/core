import { data } from "react-router";
import type { Route } from "./+types/auth.register";
import { getDb } from "~/db";
import { userFacing } from "~/services/errors.server";
import { registerWithEmail } from "~/services/auth-email.server";
import { createUserSession } from "~/services/auth.server";
import { RegisterView } from "~/views/auth/register/register-view";

export async function action({ request, context }: Route.ActionArgs) {
  const formData = await request.formData();
  const email = formData.get("email")?.toString();
  const password = formData.get("password")?.toString();
  const name = formData.get("name")?.toString();

  if (!email || !password) {
    return data({ error: "Email and password are required" }, { status: 400 });
  }

  try {
    const db = getDb();
    const user = await registerWithEmail(db, email, password, name);
    return createUserSession(user, "/app");
  } catch (error) {
    // The message a person sees is decided in one place, because both auth
    // routes used to send `error.message` straight through — and drizzle puts
    // the failing query AND its parameters in there, so a constraint violation
    // rendered a bcrypt hash into the page.
    const { message, internal } = userFacing(error);
    console.error("[register]", internal);
    return data({ error: message }, { status: 400 });
  }
}

export default function Register({ actionData }: Route.ComponentProps) {
  return <RegisterView error={actionData?.error} />;
}
