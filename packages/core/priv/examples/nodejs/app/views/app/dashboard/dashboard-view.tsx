import type { SessionUser } from "~/sessions.server";
import { useJobs } from "~/contexts/jobs";
import { Button, Avatar, AvatarImage, AvatarFallback } from "~/components";
import { JobCard } from "./job-card";

interface DashboardViewProps {
  user: SessionUser;
}

export function DashboardView({ user }: DashboardViewProps) {
  const {
    jobs: exportJobs,
    enqueueJob: queueExport,
    isConnected,
  } = useJobs("export");
  const { jobs: emailJobs, enqueueJob: queueEmail } = useJobs("email");
  const { jobs: reportJobs, enqueueJob: queueReport } = useJobs("report");

  return (
    <div className="mx-auto w-full max-w-6xl flex-1 px-6 py-12">
      <div className="flex items-center gap-4">
        <Avatar>
          <AvatarImage src={user.avatarUrl ?? undefined} alt={user.name || user.login} />
          <AvatarFallback>
            {(user.name || user.login || "?").slice(0, 1).toUpperCase()}
          </AvatarFallback>
        </Avatar>
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Background jobs</h1>
          <p className="text-sm text-muted-foreground">
            WebSocket: {isConnected ? "🟢 Connected" : "🔴 Disconnected"}
          </p>
        </div>
      </div>

      {/* One column per queue. They are independent, so they read side by side
          rather than stacked — and collapse to one column on a phone. */}
      <div className="mt-10 grid gap-6 md:grid-cols-3">
        <section className="flex flex-col gap-4">
          <Button
            variant="default"
            size="default"
            className="w-full"
            onClick={() => queueExport({ format: "csv" })}
          >
            Export data
          </Button>
          <div className="flex flex-col gap-2">
            {exportJobs.map((job) => (
              <JobCard key={job.id} job={job} type="export" />
            ))}
          </div>
        </section>

        <section className="flex flex-col gap-4">
          <Button
            variant="default"
            size="default"
            className="w-full"
            onClick={() => queueEmail({ to: "user@example.com" })}
          >
            Send email
          </Button>
          <div className="flex flex-col gap-2">
            {emailJobs.map((job) => (
              <JobCard key={job.id} job={job} type="email" />
            ))}
          </div>
        </section>

        <section className="flex flex-col gap-4">
          <Button
            variant="default"
            size="default"
            className="w-full"
            onClick={() => queueReport({ period: "monthly" })}
          >
            Generate report
          </Button>
          <div className="flex flex-col gap-2">
            {reportJobs.map((job) => (
              <JobCard key={job.id} job={job} type="report" />
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}
