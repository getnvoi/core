import { work } from "~/services/queue.server";

// The jobs process. It is the only one that works the queues, so each job runs once.
const boss = await work();

console.log("jobs ready");

process.on("SIGTERM", () => boss.stop().then(() => process.exit(0)));
