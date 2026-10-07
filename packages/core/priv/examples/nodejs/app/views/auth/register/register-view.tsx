import { Field, Button } from "~/components";

interface RegisterViewProps {
  error?: string;
}

export function RegisterView({ error }: RegisterViewProps) {
  return (
    <div className="mx-auto flex w-full max-w-sm flex-1 flex-col justify-center px-6 py-16">
      <h1 className="text-2xl font-semibold tracking-tight">Create account</h1>

      <form method="post" className="mt-8 flex flex-col gap-5">
        <Field.Root name="name">
          <Field.Label>Name (optional)</Field.Label>
          <Field.Control type="text" placeholder="John Doe" />
        </Field.Root>

        <Field.Root name="email">
          <Field.Label>Email</Field.Label>
          <Field.Control type="email" required placeholder="you@example.com" />
        </Field.Root>

        <Field.Root name="password">
          <Field.Label>Password</Field.Label>
          <Field.Control type="password" required minLength={12} />
          <Field.Description>At least 12 characters</Field.Description>
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
          Create account
        </Button>
      </form>

      <p className="mt-6 text-sm text-muted-foreground">
        Already have an account?{" "}
        <a href="/auth/login" className="font-medium text-foreground underline underline-offset-4">
          Login
        </a>
      </p>
    </div>
  );
}
