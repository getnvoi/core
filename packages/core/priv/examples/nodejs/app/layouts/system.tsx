import { Outlet, Link } from "react-router";

export default function SystemLayout() {
  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <nav className="border-b border-border">
        <div className="mx-auto flex h-16 w-full max-w-6xl items-center gap-6 px-6">
          <Link to="/system" className="flex items-baseline gap-2 text-lg font-semibold tracking-tight">
            nvoi
            <span className="rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground">
              System
            </span>
          </Link>
          <Link
            to="/system"
            className="text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            Dashboard
          </Link>
        </div>
      </nav>

      <main className="flex flex-1 flex-col">
        <Outlet />
      </main>
    </div>
  );
}
