import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import wails from '@wailsio/runtime/plugins/vite';
import { resolve } from 'node:path';

const appIconAssets = resolve('../../build/appicon.icon/Assets');

export default defineConfig({
  resolve: {
    alias: {
      $lib: resolve('./src/lib')
    }
  },
  server: {
    host: '127.0.0.1',
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
    fs: {
      allow: [resolve('.'), appIconAssets]
    }
  },
  build: {
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]'
      }
    }
  },
  plugins: [tailwindcss(), svelte(), wails('./bindings')]
});
