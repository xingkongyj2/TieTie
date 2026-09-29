import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import { qoderPlugin } from './server/vite-plugin.mjs';

export default defineConfig(({ mode }) => ({
  plugins: [react(), qoderPlugin(loadEnv(mode, '.', 'QODER_'))],
  server: { host: '127.0.0.1' },
  preview: { host: '127.0.0.1' },
}));
