import { defineConfig } from "vitest/config";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  plugins: [tsconfigPaths()],
  test: {
    globals: true,
    environment: "happy-dom",
    setupFiles: ["./app/tests/setup.ts"],
    include: ["app/tests/**/*.test.ts"],
    testTimeout: 15_000,
  },
});
