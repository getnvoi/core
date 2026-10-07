import type { Job } from "~/contexts/jobs";

interface JobCardProps {
  job: Job;
  type: "export" | "email" | "report";
}

const statusMessages = {
  export: {
    queued: "⏳ Queued...",
    started: "⚙️ Exporting...",
    completed: (result: string | null) =>
      `✅ Done: ${JSON.parse(result || "{}").file || "export.csv"}`,
    error: (error: string | null) => `❌ Error: ${error}`,
  },
  email: {
    queued: "⏳ Queued...",
    started: "⚙️ Sending...",
    completed: () => "✅ Sent",
    error: (error: string | null) => `❌ Failed: ${error}`,
  },
  report: {
    queued: "⏳ Queued...",
    started: "⚙️ Generating...",
    completed: (result: string | null) =>
      `✅ Ready: ${JSON.parse(result || "{}").report || "report.pdf"}`,
    error: (error: string | null) => `❌ Failed: ${error}`,
  },
};

export function JobCard({ job, type }: JobCardProps) {
  const messages = statusMessages[type];

  let statusContent: string;
  if (job.status === "completed") {
    statusContent = messages.completed(job.result || null);
  } else if (job.status === "error") {
    statusContent = messages.error(job.error || null);
  } else {
    statusContent = messages[job.status] as string;
  }

  return (
    // `data-status` drives the border colour, so the state a card is in is
    // readable from the DOM as well as from its text — which is what a test
    // asserts on.
    <div
      data-status={job.status}
      className="flex items-center justify-between gap-3 rounded-md border border-border bg-card px-3 py-2 text-sm data-[status=error]:border-destructive/40"
    >
      <span>{statusContent}</span>
      <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
        {new Date(job.createdAt).toLocaleTimeString()}
      </span>
    </div>
  );
}
