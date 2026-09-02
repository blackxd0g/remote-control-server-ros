import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:21114',
      '/healthz': 'http://127.0.0.1:21114',
    },
  },
  build: {
    outDir: '../art-api/internal/webui/dist',
    emptyOutDir: true,
  },
})
