import { reactRouter } from "@react-router/dev/vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  plugins: [tailwindcss(), reactRouter(), tsconfigPaths()],
  // Bundled before the first request so a new dependency does not force a reload mid-render.
  optimizeDeps: {
    include: ["drizzle-orm", "drizzle-orm/node-postgres", "drizzle-orm/pg-core"],
  },
  server: {
    // The dev server is reached through a preview hostname that differs per box.
    allowedHosts: true,
  },
});
