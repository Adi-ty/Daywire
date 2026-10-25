import { defineConfig } from 'astro/config';
import { fileURLToPath } from 'node:url';
import { contentDirectory } from './scripts/content-path.mjs';
import { watchEditions } from './scripts/watch-editions.mjs';

export default defineConfig({
  output: 'static',
  site: process.env.SITE_URL || undefined,
  base: process.env.BASE_PATH || '/',
  trailingSlash: 'always',
  devToolbar: { enabled: false },
  vite: {
    plugins: [watchEditions(contentDirectory())],
    define: { 'process.env.DAYWIRE_PROJECT_ROOT': JSON.stringify(fileURLToPath(new URL('../', import.meta.url))) },
  },
});
