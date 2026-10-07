import { reactRouter } from "@react-router/dev/vite";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [
    reactRouter(),
    tsconfigPaths(),
    tailwindcss(),
  ],
  // BUNDLED AT STARTUP, not discovered mid-render.
  //
  // Vite does not know what the SSR entry imports until it renders one. The
  // first request pulls in `app/db/schema.ts` → `drizzle-orm`, so Vite optimises
  // it and bumps the pre-bundle hash — while the module runner mid-render is
  // still holding the old handle:
  //
  //   [vite] ✨ new dependencies optimized: drizzle-orm, drizzle-orm/better-sqlite3
  //   [vite] An error happened during full reload
  //   Error: There is a new version of the pre-bundle for
  //     ".../deps_ssr/drizzle-orm.js?v=3dddb343"  at app/db/schema.ts:2:1
  //
  // It throws, forces a full reload, and that reload discovers the NEXT
  // entrypoint — `/better-sqlite3`, then `/sqlite-core` — so each one
  // invalidates again. In a preview the whole cascade reads as an app that dies
  // on refresh.
  //
  // Naming the three the app actually imports collapses it to one optimise pass
  // before the first request. They are listed rather than globbed because the
  // failure is per ENTRYPOINT: `drizzle-orm` and `drizzle-orm/better-sqlite3`
  // are separate pre-bundles and each is its own chance to invalidate.
  optimizeDeps: {
    include: [
      "drizzle-orm",
      "drizzle-orm/better-sqlite3",
      "drizzle-orm/better-sqlite3/migrator",
      "drizzle-orm/sqlite-core",
    ],

    // NEVER PRE-BUNDLED. It is a NATIVE addon — Vite's optimiser cannot process
    // a `.node` binary, and bundling the JS wrapper away from it breaks the
    // `bindings` lookup that finds it.
    exclude: ["better-sqlite3"],
  },

  // VITE IS IN MIDDLEWARE MODE (see `server/app.ts`), so it binds nothing — but
  // these still apply, because express hands it every request and Vite decides
  // what to do with the Host header and where to tell HMR to reconnect.
  server: {

    // Bound to every interface, because this app is developed inside a container
    // and reached from outside it. On loopback the dev server is unreachable from
    // anything but the container itself.
    host: true,

    // THE PREVIEW HOSTNAME. Vite refuses a Host header it was not told about —
    // a defence against DNS rebinding — and answers "Blocked request. This host
    // is not allowed", which reads as a broken proxy rather than as a setting.
    //
    // A worktree is served at `<slug>.nvoi.to`, and the slug differs per
    // worktree, so the suffix is what can be named here. A leading dot matches
    // the domain and its subdomains.
    allowedHosts: [".nvoi.to", "localhost"],

    // HMR IS A WEBSOCKET, AND IT IS NOT SERVED WHERE THE BROWSER THINKS.
    //
    // Vite tells the client where to reconnect, and by default that is the
    // address the dev server bound — `localhost:3000`. A page opened through a
    // preview then tries `wss://localhost:3000` from somebody else's machine
    // and fails on every reload:
    //
    //   [vite] failed to connect to websocket.
    //     (browser) <preview host>/ <--[WebSocket (failing)]--> localhost:3000/
    //
    // FROM THE ENVIRONMENT, because only the environment knows. A sandbox serves
    // this app through TLS on 443 under a hostname it cannot guess; a laptop
    // serves it on 3000 over ws. Hardcoding the first breaks the second.
    //
    // The HOST is deliberately not set: the client keeps
    // `window.location.hostname`, so one image serves every worktree.
    //
    // Unset means Vite's own default, which is correct on a laptop — this is a
    // deployment fact, not a missing value.
    ...(process.env.HMR_CLIENT_PORT || process.env.HMR_PROTOCOL
      ? {
          hmr: {
            ...(process.env.HMR_CLIENT_PORT
              ? { clientPort: Number(process.env.HMR_CLIENT_PORT) }
              : {}),
            ...(process.env.HMR_PROTOCOL ? { protocol: process.env.HMR_PROTOCOL } : {}),
          },
        }
      : {}),
  },
});
