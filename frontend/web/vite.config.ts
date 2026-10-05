import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 开发/预览时 /api 统一代理到 Go 后端（../../backend，默认 127.0.0.1:4173）。
// 启动顺序：先在 ../../backend/ 里 `go run ./cmd/server`（或 frontend/ 里 `npm start`），再 `npm run dev`。
const backend = { '/api': 'http://127.0.0.1:4173' }

export default defineConfig({
  plugins: [react()],
  server: { host: '127.0.0.1', proxy: backend },
  preview: { host: '127.0.0.1', proxy: backend },
})
