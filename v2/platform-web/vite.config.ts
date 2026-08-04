import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    manifest: true,
    rollupOptions: { output: { entryFileNames: "assets/platform-[hash].js", chunkFileNames: "assets/platform-[hash].js", assetFileNames: "assets/platform-[hash][extname]" } },
  },
})
