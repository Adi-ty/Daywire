// Local convenience runner: Node parses .env as data, never as shell commands.
// CI can continue calling the Go CLI directly with injected environment variables.
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
try {
  process.loadEnvFile(fileURLToPath(new URL('../.env', import.meta.url)));
} catch (error) {
  if (error.code !== 'ENOENT') {
    console.error('daywire: could not load .env');
    process.exit(1);
  }
}
const result = spawnSync('go', ['run', './cmd/daywire', ...process.argv.slice(2)], {
  cwd: root,
  env: process.env,
  stdio: 'inherit',
});
if (result.error) console.error('daywire: could not start Go; check that it is installed');
process.exit(result.status ?? 1);
