import type { Config } from "@react-router/dev/config";

// NVOI_STATIC=1 (`npm run build:static`) builds a single-page app: no server, no database, every route
// rendered in the browser into build/client. An artifact of kind app is built this way.
export const STATIC = process.env.NVOI_STATIC === "1";

export default {
  ssr: !STATIC,
  future: {
    v8_middleware: true,
    v8_passThroughRequests: true,
    v8_splitRouteModules: true,
    v8_trailingSlashAwareDataRequests: true,
    v8_viteEnvironmentApi: true,
  },
} satisfies Config;
