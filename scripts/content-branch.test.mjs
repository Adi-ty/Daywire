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

test('Vercel source export restores public archive without .git or origin', () => {
  const dir = mkdtempSync(join(tmpdir(), 'daywire-public-archive-test-'));
  const git = (cwd, ...args) => execFileSync('git', args, { cwd, stdio: 'pipe' });
  try {
    const origin = join(dir, 'origin.git'), seed = join(dir, 'seed');
    git(dir, 'init', '--bare', origin);
    mkdirSync(seed);
    git(seed, 'init', '-b', 'content');
    git(seed, 'config', 'user.name', 'Test');
    git(seed, 'config', 'user.email', 'test@example.com');
    mkdirSync(join(seed, 'editions'));
    writeFileSync(join(seed, 'editions/2026-10-26.json'), JSON.stringify({ date: '2026-10-26' }));
    git(seed, 'add', 'editions');
    git(seed, 'commit', '-m', 'Public archive');
    git(seed, 'remote', 'add', 'origin', origin);
    git(seed, 'push', 'origin', 'content');
    const env = {
      ...process.env,
      // Redirect only this public repository URL to an actual local Git remote.
      // Exercise git clone/archive without network access or a mocked Git binary.
      GIT_CONFIG_COUNT: '1',
      GIT_CONFIG_KEY_0: `url.file://${origin}.insteadOf`,
      GIT_CONFIG_VALUE_0: 'https://github.com/example/daywire.git',
      DAYWIRE_CONTENT_REPOSITORY: '',
      VERCEL_GIT_REPO_OWNER: 'example',
      VERCEL_GIT_REPO_SLUG: 'daywire',
    };
    for (const mode of ['system-vars', 'explicit-repository']) {
      const runner = join(dir, mode);
      mkdirSync(runner); // Deliberately never git init or add an origin.
      const settings = mode === 'explicit-repository'
        ? { ...env, DAYWIRE_CONTENT_REPOSITORY: 'example/daywire', VERCEL_GIT_REPO_OWNER: '', VERCEL_GIT_REPO_SLUG: '' }
        : env;
      execFileSync('bash', [script, 'restore-public'], { cwd: runner, env: settings, stdio: 'pipe' });
      assert.deepEqual(readdirSync(runner), ['content']);
      assert.equal(JSON.parse(readFileSync(join(runner, 'content/editions/2026-10-26.json'))).date, '2026-10-26');
    }
    const missing = join(dir, 'missing-branch');
    mkdirSync(missing);
    git(dir, '--git-dir', origin, 'update-ref', '-d', 'refs/heads/content');
    assert.throws(() => execFileSync('bash', [script, 'restore-public'], { cwd: missing, env, stdio: 'pipe' }));
    assert.deepEqual(readdirSync(missing), [], 'failed fetch must not become an empty production archive');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
