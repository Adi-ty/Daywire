import { existsSync, readdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { contentDirectory, root } from './content-path.mjs';

const directory = contentDirectory();
if (existsSync(directory) && readdirSync(directory).some(name => name.endsWith('.json'))) {
  execFileSync('go', ['run', './cmd/daywire', 'validate', '--out', directory], { cwd: root, stdio: 'inherit' });
} else {
  console.log('No editions yet. Building the honest empty state. Set DAYWIRE_DEMO=1 for the fictional preview.');
}
