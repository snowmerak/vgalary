import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';
import { writeFile } from 'node:fs/promises';

const assets = fileURLToPath(new URL('../internal/webui/assets/', import.meta.url));

export default defineConfig({
  plugins: [
    react(),
    {
      name: 'preserve-embed-directory',
      async closeBundle() {
        await writeFile(`${assets}/.gitkeep`, '');
      },
    },
  ],
  build: { outDir: assets, emptyOutDir: true },
  server: { proxy: { '/api': 'http://127.0.0.1:8181' } },
});
