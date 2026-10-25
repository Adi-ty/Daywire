import test from 'node:test';
import assert from 'node:assert/strict';
import { contentDirectory, root } from './content-path.mjs';
import path from 'node:path';

test('production never silently falls back to demo data', () => {
  assert.equal(contentDirectory({}), path.join(root, 'content/editions'));
  assert.equal(contentDirectory({ DAYWIRE_DEMO: '1' }), path.join(root, 'testdata/demo'));
  assert.throws(() => contentDirectory({ DAYWIRE_DEMO: '1', DAYWIRE_CONTENT_DIR: '/tmp/live' }));
});
