// Runs `codex exec --json` and serves it nvoi's MCP tools over this process's own stdin and stdout, as Claude
// Code's stream-json does: the server reads one stream and answers on one pipe, whichever harness runs.
//
//   node bridge.cjs run <socket> <codex argv…>   the turn: the prompt is the first line on stdin, appended to argv
//   node bridge.cjs connect <socket>             the MCP server Codex starts: relays its stdio to the socket
//
// Codex's lines pass through; its other output becomes {"type":"stderr"}; each MCP request from Codex becomes a
// control_request with subtype mcp_message, answered by a control_response on stdin. Notifications are dropped.
const net = require("net");
const fs = require("fs");
const readline = require("readline");
const { spawn } = require("child_process");

const [mode, socket, ...argv] = process.argv.slice(2);

if (mode === "connect") {
  const conn = net.connect(socket);
  process.stdin.pipe(conn);
  conn.pipe(process.stdout);
  conn.on("close", () => process.exit(0));
  conn.on("error", (e) => { process.stderr.write(`nvoi bridge: ${e.message}\n`); process.exit(1); });
} else if (mode === "run") {
  const out = (frame) => process.stdout.write(JSON.stringify(frame) + "\n");
  const waiting = new Map();
  let next = 0;
  let child = null;

  try { fs.unlinkSync(socket); } catch {}
  const server = net.createServer((conn) => {
    readline.createInterface({ input: conn }).on("line", (line) => {
      let message;
      try { message = JSON.parse(line); } catch { return; }
      if (!("id" in message)) return;
      const rid = `mcp-${++next}`;
      waiting.set(rid, conn);
      out({ type: "control_request", request_id: rid, request: { subtype: "mcp_message", server_name: "nvoi", message } });
    });
    conn.on("error", () => {});
  });
  server.listen(socket);

  readline.createInterface({ input: process.stdin }).on("line", (line) => {
    let frame;
    try { frame = JSON.parse(line); } catch { return; }
    if (frame.type === "user" && !child) return start(String(frame.message && frame.message.content));
    const response = frame.type === "control_response" && frame.response;
    const conn = response && waiting.get(response.request_id);
    if (!conn) return;
    waiting.delete(response.request_id);
    const answer = response.response && response.response.mcp_response;
    if (answer) conn.write(JSON.stringify(answer) + "\n");
  });

  function start(prompt) {
    child = spawn(argv[0], [...argv.slice(1), prompt], { stdio: ["ignore", "pipe", "pipe"] });
    readline.createInterface({ input: child.stdout }).on("line", (line) => {
      if (line.startsWith("{")) process.stdout.write(line + "\n");
      else if (line.trim()) out({ type: "stderr", text: line });
    });
    readline.createInterface({ input: child.stderr }).on("line", (line) => {
      if (line.trim()) out({ type: "stderr", text: line });
    });
    child.on("error", (e) => { out({ type: "error", message: `codex did not start: ${e.message}` }); finish(127); });
    child.on("close", (code, signal) => finish(code === null ? 128 + (signal === "SIGKILL" ? 9 : 15) : code));
  }

  function finish(code) {
    server.close();
    try { fs.unlinkSync(socket); } catch {}
    process.stdout.write("", () => process.exit(code));
  }

  for (const s of ["SIGTERM", "SIGINT"]) process.on(s, () => (child ? child.kill(s) : finish(143)));
}
