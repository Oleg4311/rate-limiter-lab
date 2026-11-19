// Проверка WASM-сборки без браузера: ratelabRun возвращает график и ожидаемые числа.
// Использование: node scripts/wasm-smoke.mjs <каталог-с-wasm_exec.js-и-ratelab.wasm>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join, resolve } from 'node:path';

const dir = resolve(process.argv[2] ?? 'site');
createRequire(import.meta.url)(join(dir, 'wasm_exec.js'));
const go = new Go();
const { instance } = await WebAssembly.instantiate(readFileSync(join(dir, 'ratelab.wasm')), go.importObject);
go.run(instance);

const patterns = JSON.parse(globalThis.ratelabPatterns());
assert.deepEqual(patterns.map((p) => p.id), ['steady', 'burst', 'boundary', 'poisson', 'ramp']);

const ok = JSON.parse(globalThis.ratelabRun(JSON.stringify({ pattern: 'boundary', limit: 100, windowMs: 1000, burst: 0, rate: 200, durationMs: 6000, seed: 7 })));
assert.equal(ok.error, undefined);
const by = Object.fromEntries(ok.result.algorithms.map((a) => [a.name, a]));
assert.equal(by['fixed-window'].maxInWindow, 200, 'fixed-window пропускает 2×лимит на границе окна');
assert.equal(by['sliding-log'].maxInWindow, 100);
assert.match(ok.svg, /^<svg[\s\S]*<\/svg>$/);

const bad = JSON.parse(globalThis.ratelabRun(JSON.stringify({ pattern: 'boundary', limit: 0, windowMs: 1000, durationMs: 6000 })));
assert.match(bad.error, /limit/);
const garbage = JSON.parse(globalThis.ratelabRun('{не json'));
assert.match(garbage.error, /некорректные параметры/);
console.log('wasm-smoke: OK');
