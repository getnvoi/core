import {
  type RouteConfig,
  route,
  layout,
  index,
} from "@react-router/dev/routes";

export default [
  // Public routes
  layout("layouts/public.tsx", [index("routes/home.tsx"), route("about", "routes/about.tsx")]),

  // Protected app routes (all nested under /app)
  layout("layouts/app.tsx", [
    route("app", "routes/app/index.tsx"),
    route("app/api/jobs", "routes/app/api.jobs.tsx"),
    // `/app/jobs/ws` IS NOT A ROUTE. A WebSocket upgrade never reaches React
    // Router — `server/app.ts` intercepts it on the HTTP server, because a
    // fetch `Response` cannot carry a socket.
    // Add more protected routes here as needed
    // route("app/settings", "routes/app/settings.tsx"),
  ]),

  // Email auth. The boilerplate shipped four OAuth providers, invites and
  // Postmark email; a seed is a starting point, not a SaaS.
  route("auth/login", "routes/auth.login.tsx"),
  route("auth/register", "routes/auth.register.tsx"),
  route("logout", "routes/logout.tsx"),
  route("system", "routes/system.tsx"),
] satisfies RouteConfig;
