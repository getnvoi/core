import { Field, Button } from "~/components";

interface LoginViewProps {
  error?: string;
}

export function LoginView({ error }: LoginViewProps) {
  return (
    <div className="mx-auto flex w-full max-w-sm flex-1 flex-col justify-center px-6 py-16">
      <h1 className="text-2xl font-semibold tracking-tight">Login</h1>

      <form method="post" className="mt-8 flex flex-col gap-5">
        <Field.Root name="email">
          <Field.Label>Email</Field.Label>
          <Field.Control type="email" required placeholder="you@example.com" />
        </Field.Root>

        <Field.Root name="password">
          <Field.Label>Password</Field.Label>
          <Field.Control type="password" required />
        </Field.Root>

        {error && (
          <div
            role="alert"
            className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </div>
        )}

        <Button type="submit" className="w-full">
          Login
        </Button>
      </form>

      <p className="mt-6 text-sm text-muted-foreground">
        Don't have an account?{" "}
        <a href="/auth/register" className="font-medium text-foreground underline underline-offset-4">
          Sign up
        </a>
      </p>
    </div>
  );
}
