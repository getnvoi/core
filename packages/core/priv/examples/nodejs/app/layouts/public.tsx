import { Outlet, Link } from "react-router";
import { Button } from "~/components";

/**
 * The shell around every signed-out page.
 *
 * Classes use the theme tokens declared in `app.css` — `background`,
 * `foreground`, `border` — rather than raw colours, so a worktree that edits the
 * theme changes this with it. That is what the tokens are for.
 */
export default function PublicLayout() {
  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <nav className="border-b border-border">
        <div className="mx-auto flex h-16 w-full max-w-5xl items-center justify-between px-6">
          <Link to="/" className="text-lg font-semibold tracking-tight">
            nvoi
          </Link>
          <Button size="default" asChild>
            <Link to="/auth/login">Log in</Link>
          </Button>
        </div>
      </nav>

      <main className="flex flex-1 flex-col">
        <Outlet />
      </main>
    </div>
  );
}
