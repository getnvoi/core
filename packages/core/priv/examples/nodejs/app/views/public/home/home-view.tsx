import { Link } from "react-router";
import { Button } from "~/components";
import type { SessionUser } from "~/sessions.server";

interface HomeViewProps {
  user: SessionUser | null;
}

export function HomeView({ user }: HomeViewProps) {
  return (
    <div className="mx-auto flex w-full max-w-5xl flex-1 flex-col items-center justify-center px-6 py-24 text-center">
      <h1 className="text-4xl font-semibold tracking-tight sm:text-5xl">Your app</h1>

      <p className="mt-4 max-w-prose text-lg text-muted-foreground">
        {user
          ? `Signed in as ${user.login || user.name}`
          : "Sign in to continue."}
      </p>

      <div className="mt-10 flex flex-col gap-3 sm:flex-row">
        {user ? (
          <Button size="lg" asChild>
            <Link to="/app">Open the app</Link>
          </Button>
        ) : (
          <>
            <Button size="lg" asChild>
              <Link to="/auth/login">Log in</Link>
            </Button>
            <Button size="lg" variant="outline" asChild>
              <Link to="/auth/register">Register</Link>
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
