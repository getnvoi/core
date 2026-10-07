// Builds the player into one file, dist/index.html, which nvoi serves as the video's preview.
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

export default defineConfig({
  root: "player",
  plugins: [react(), viteSingleFile()],
  build: { outDir: "../dist", emptyOutDir: true, rollupOptions: { input: "player/index.html" } },
});
