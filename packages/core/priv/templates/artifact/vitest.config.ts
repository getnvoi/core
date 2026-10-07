import tsconfigPaths from "vite-tsconfig-paths";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [tsconfigPaths()],
  test: {
    environment: "node",
    setupFiles: ["./app/tests/setup.ts"],
    include: ["app/tests/**/*.test.ts"],
  },
});
