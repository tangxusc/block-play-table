import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

const pagesRepository = process.env.GITHUB_REPOSITORY?.split("/")[1] || "";
const pagesBase = process.env.GITHUB_PAGES === "true" && pagesRepository ? `/${pagesRepository}/` : "./";

export default defineConfig({
  root: __dirname,
  base: pagesBase,
  plugins: [vue()],
  server: {
    host: "127.0.0.1",
    port: 3000,
    strictPort: true,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
