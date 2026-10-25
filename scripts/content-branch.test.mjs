import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

const script = fileURLToPath(new URL('./content-branch.sh', import.meta.url));

test('fresh CI checkout restores all public editions from the content branch', () => {
  const dir = mkdtempSync(join(tmpdir(), 'daywire-archive-test-'));
  const git = (cwd, ...args) => execFileSync('git', args, { cwd, stdio: 'pipe' });
  try {
    const origin = join(dir, 'origin.git'), seed = join(dir, 'seed'), runner = join(dir, 'runner');
    git(dir, 'init', '--bare', origin);
    mkdirSync(seed);
    git(seed, 'init', '-b', 'content');
    git(seed, 'config', 'user.name', 'Test');
    git(seed, 'config', 'user.email', 'test@example.com');
    mkdirSync(join(seed, 'editions'));
    for (const date of ['2026-10-23', '2026-10-24']) {
      writeFileSync(join(seed, 'editions', `${date}.json`), JSON.stringify({ date }));
    }
    git(seed, 'add', 'editions');
    git(seed, 'commit', '-m', 'Fixture archive');
    git(seed, 'remote', 'add', 'origin', origin);
    git(seed, 'push', 'origin', 'content');
    mkdirSync(runner);
    git(runner, 'init');
    git(runner, 'remote', 'add', 'origin', origin);
    execFileSync('bash', [script, 'restore'], { cwd: runner, stdio: 'pipe' });
    const editions = join(runner, 'content/editions');
    assert.deepEqual(readdirSync(editions).sort(), ['2026-10-23.json', '2026-10-24.json']);
    assert.equal(JSON.parse(readFileSync(join(editions, '2026-10-24.json'))).date, '2026-10-24');
    assert.equal(git(runner, 'diff', '--cached', '--name-only').toString(), '');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
