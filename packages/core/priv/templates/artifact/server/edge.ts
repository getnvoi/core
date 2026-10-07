import type { Express } from "express";

// The app is reached only through nvoi's edge. The edge says the page's address in x-nvoi-host, because a
// vendor's edge in between may rewrite X-Forwarded-Host to the machine's own. React Router refuses a form
// posted from the page unless the request's URL (built from X-Forwarded-Host and X-Forwarded-Proto once
// the proxy is trusted) has the page's origin. Host is set too: without a port in X-Forwarded-Host, the
// adapter takes the port from Host, which is the machine's (":8080" on a server) and would not match.
// Do not change this: app/tests/edge.test.ts proves it.
export function edge(app: Express) {
  app.set("trust proxy", true);
  app.use((req, _res, next) => {
    const host = req.get("x-nvoi-host");
    if (host) {
      req.headers["x-forwarded-host"] = host;
      req.headers.host = host;
    }
    next();
  });
}
