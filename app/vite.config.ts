import { defineConfig } from 'vite';

export default defineConfig({
  base: './',
  build: { target: 'es2022' },
  // The desktop shell expects the dev server on this exact port.
  server: { port: 5173, strictPort: true },
  clearScreen: false,
});
