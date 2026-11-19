// Статический сервер для e2e: отдаёт ../site на 127.0.0.1:4177. Сайт собирается заранее: node scripts/build-site.mjs
import { createReadStream, existsSync, statSync } from 'node:fs';
import { createServer } from 'node:http';
import { extname, join, normalize, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json',
  '.wasm': 'application/wasm',
  '.log': 'text/plain; charset=utf-8',
  '.png': 'image/png',
};
const root = resolve(fileURLToPath(new URL('../site', import.meta.url)));
if (!existsSync(root)) throw new Error('нет каталога site/: выполните node scripts/build-site.mjs');

createServer((req, res) => {
  const path = normalize(decodeURIComponent(new URL(req.url, 'http://x').pathname)).replace(/^([/\\])+/, '');
  let file = join(root, path);
  if (!file.startsWith(root)) return res.writeHead(403).end();
  if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html');
  if (!existsSync(file)) return res.writeHead(404).end('not found');
  res.writeHead(200, { 'content-type': TYPES[extname(file)] ?? 'application/octet-stream', 'cache-control': 'no-store' });
  createReadStream(file).pipe(res);
}).listen(4177, '127.0.0.1', () => console.log('http://127.0.0.1:4177/'));
