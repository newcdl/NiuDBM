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
    // Element Plus 全量引入后体积不小，单独成包让业务代码改动时还能吃缓存
    chunkSizeWarningLimit: 1100,
    rollupOptions: {
      output: {
        manualChunks(id: string): string | undefined {
          if (id.includes('node_modules/element-plus') || id.includes('node_modules/@element-plus')) {
            return 'element-plus'
          }
          if (id.includes('node_modules/@vue') || id.includes('node_modules/vue')) return 'vue'
          return undefined
        },
      },
    },
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
