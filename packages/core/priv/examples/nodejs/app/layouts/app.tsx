import type { Route } from "./+types/app";
import { Outlet, Link, Form } from "react-router";
import { requireUser } from "~/services/auth.server";
import { JobsProvider } from "~/contexts/jobs";
import { getDb, jobs as jobsTable } from "~/db";
import { eq, desc } from "drizzle-orm";
import { Button, Avatar, AvatarImage, AvatarFallback } from "~/components";

export async function loader({ request, context }: Route.LoaderArgs) {
  const user = await requireUser(request);
  const db = getDb();

  // Load user's jobs from DB
  const userJobs = await db
    .select()
    .from(jobsTable)
    .where(eq(jobsTable.userId, user.id))
    .orderBy(desc(jobsTable.createdAt))
    .limit(50);

  return { user, initialJobs: userJobs };
}

export default function AppLayout({ loaderData }: Route.ComponentProps) {
  const { user, initialJobs } = loaderData;

  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <nav className="border-b border-border">
        <div className="mx-auto flex h-16 w-full max-w-6xl items-center justify-between gap-6 px-6">
          <div className="flex items-center gap-6">
            <Link to="/" className="text-lg font-semibold tracking-tight">
              nvoi
            </Link>
            <Link
              to="/app"
              className="text-sm text-muted-foreground transition-colors hover:text-foreground"
            >
              Dashboard
            </Link>
          </div>

          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2">
              <Avatar>
                <AvatarImage
                  src={user.avatarUrl ?? undefined}
                  alt={user.name || user.login}
                />
                <AvatarFallback>
                  {(user.name || user.login || "?").slice(0, 1).toUpperCase()}
                </AvatarFallback>
              </Avatar>
              {/* Hidden on small screens: the avatar already identifies them, and
                  a long name pushes the sign-out button off a phone. */}
              <span className="hidden text-sm font-medium sm:inline">
                {user.name || user.login}
              </span>
            </div>
            <Form method="post" action="/logout">
              <Button variant="secondary" size="default" type="submit">
                Logout
              </Button>
            </Form>
          </div>
        </div>
      </nav>

      <main className="flex flex-1 flex-col">
        <JobsProvider initialJobs={initialJobs}>
          <Outlet context={{ user }} />
        </JobsProvider>
      </main>
    </div>
  );
}
