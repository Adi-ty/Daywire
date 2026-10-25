import { fileURLToPath } from 'node:url';
import path from 'node:path';

// Astro bundles this module into dist/.prerender. Pin the build-time project
// directory in astro.config instead of resolving relative to that output file.
export const root = process.env.DAYWIRE_PROJECT_ROOT || fileURLToPath(new URL('../../', import.meta.url));
export function contentDirectory(env = process.env) {
  const demo = env.DAYWIRE_DEMO === '1';
  if (demo && env.DAYWIRE_CONTENT_DIR) throw new Error('Choose demo mode or a content directory, not both.');
  return path.resolve(root, env.DAYWIRE_CONTENT_DIR || (demo ? 'testdata/demo' : 'content/editions'));
}
