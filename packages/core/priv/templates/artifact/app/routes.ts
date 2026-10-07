import { type RouteConfig, index } from "@react-router/dev/routes";

// The WebSocket at /socket is not a route: server/app.ts takes the upgrade. Built static (NVOI_STATIC=1),
// routes run in the browser only and may not have a loader or an action: routes/page.tsx is the example.
export default [
  index(process.env.NVOI_STATIC === "1" ? "routes/page.tsx" : "routes/home.tsx"),
] satisfies RouteConfig;
