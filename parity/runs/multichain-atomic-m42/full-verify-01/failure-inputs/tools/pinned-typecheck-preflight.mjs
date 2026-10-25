// Fail before expensive acceptance work when Vitest cannot spawn the pinned tsc.
import fs from 'node:fs';
import path from 'node:path';
import {spawnSync} from 'node:child_process';

export function assertPinnedTypechecker(upstream, env = process.env) {
 const expected = path.resolve(upstream, 'node_modules/.bin/tsc');
 const resolved = (env.PATH ?? '').split(path.delimiter).map(dir => path.resolve(dir, 'tsc')).find(file => {
  try { fs.accessSync(file, fs.constants.X_OK); return fs.statSync(file).isFile(); } catch { return false; }
 });
 if (!resolved) throw Error('Pinned TypeScript compiler is missing from PATH; add the pinned upstream node_modules/.bin before acceptance');
 if (fs.realpathSync(resolved) !== fs.realpathSync(expected)) throw Error('PATH resolves a different TypeScript compiler than the pinned upstream installation');
 const version = JSON.parse(fs.readFileSync(path.resolve(upstream, 'node_modules/typescript/package.json'), 'utf8')).version;
 const result = spawnSync('tsc', ['--version'], {env, encoding:'utf8'});
 if (result.error || result.status !== 0 || result.stdout.trim() !== `Version ${version}`) throw Error('Pinned TypeScript compiler version check failed');
 return {compiler:resolved, version, exit_code:result.status};
}
