// Собирает демо для GitHub Pages в site/: wasm, wasm_exec.js, страница и version.json с коммитом.
import { execFileSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

rmSync('site', { recursive: true, force: true });
mkdirSync('site');

execFileSync('go', ['build', '-o', join('site', 'ratelab.wasm'), './cmd/wasm'], {
  stdio: 'inherit',
  env: { ...process.env, GOOS: 'js', GOARCH: 'wasm' },
});

const goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
const execJs = [join(goroot, 'lib', 'wasm', 'wasm_exec.js'), join(goroot, 'misc', 'wasm', 'wasm_exec.js')].find(existsSync);
if (!execJs) throw new Error('wasm_exec.js не найден в GOROOT');
copyFileSync(execJs, join('site', 'wasm_exec.js'));
copyFileSync(join('web', 'index.html'), join('site', 'index.html'));

let commit = process.env.GITHUB_SHA;
if (!commit) {
  try {
    commit = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
  } catch {
    commit = undefined;
  }
}
if (commit) writeFileSync(join('site', 'version.json'), JSON.stringify({ commit, version: '0.1.0', builtAt: new Date().toISOString() }, null, 2) + '\n');
console.log('site/ готов');
