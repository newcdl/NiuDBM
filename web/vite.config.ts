import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 前端产物直接落到 Go 的 embed 目录，避免构建后再拷贝
const OUT_DIR = fileURLToPath(new URL('../server/internal/webui/dist', import.meta.url))

export default defineConfig({
  plugins: [vue()],
  // 网关下页面挂在 /app/niudbm/，静态资源必须用相对路径，配合后端注入的 <base>
  base: './',
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: OUT_DIR,
    emptyOutDir: true,
    // 单文件体积可控，不做过度拆分，减少网关下的请求数
    chunkSizeWarningLimit: 800,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8780',
        changeOrigin: false,
      },
    },
  },
})
