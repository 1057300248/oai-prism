import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  base: './',
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        // 大依赖分包：业务代码改动不再打爆 vendor 缓存，
        // antd/x/plots 各自独立，首屏只需下载当前页需要的部分。
        manualChunks(id: string) {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('@ant-design/plots') || id.includes('@antv')) return 'vendor-plots'
          if (id.includes('@ant-design/x')) return 'vendor-antdx'
          if (id.includes('antd') || id.includes('@ant-design/icons') || id.includes('rc-')) return 'vendor-antd'
          if (id.includes('react') || id.includes('scheduler')) return 'vendor-react'
          return 'vendor-misc'
        },
      },
    },
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: {
      '/v1': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
      '/metrics': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
      '/healthz': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
      '/admin': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
      },
    },
  },
})
