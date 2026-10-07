import express from "express";

// The static build (npm run build:static) served on $PORT, every path falling back to index.html, where the
// routes run in the browser. No database and no jobs: an artifact of kind app runs this way.
const PORT = Number(process.env.PORT ?? 3000);

const app = express();
app.disable("x-powered-by");
app.use("/assets", express.static("build/client/assets", { immutable: true, maxAge: "1y" }));
app.use(express.static("build/client"));
app.get("*", (_req, res) => res.sendFile("index.html", { root: "build/client" }));

const server = app.listen(PORT, "0.0.0.0", () => console.log(`static on :${PORT}`));

process.on("SIGTERM", () => server.close(() => process.exit(0)));
