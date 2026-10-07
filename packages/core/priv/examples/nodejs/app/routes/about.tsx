import { Link } from "react-router";

export function meta() {
  return [
    { title: "About · nvoi" },
    { name: "description", content: "What runs this project." },
  ];
}

/**
 * A SECOND PUBLIC ROUTE, so "it routes" is a claim with evidence.
 *
 * The other three seeds have `/` and `/about`, and this one had `/` plus
 * everything else behind a login — so an assertion that every seed serves two
 * pages could not be written against all four without special-casing this one.
 * A check that skips a seed is a check that seed does not have.
 */
export default function About() {
  return (
    <main className="mx-auto max-w-2xl px-6 py-16">
      <nav className="mb-8 flex gap-4 text-sm">
        <Link to="/" className="text-muted-foreground hover:text-foreground">
          Home
        </Link>
        <Link to="/about" className="font-medium text-foreground">
          About
        </Link>
      </nav>

      <h1 className="text-2xl font-semibold tracking-tight">About</h1>

      <p className="mt-4 text-sm text-muted-foreground">
        A React Router application on Bun, with its own Durable Object for
        websockets and a job queue behind <code>/app</code>.
      </p>

      <p className="mt-4 text-sm text-muted-foreground">
        Signing in reaches the rest: the jobs page enqueues work, a worker
        records it, and every connected browser is told.
      </p>
    </main>
  );
}
