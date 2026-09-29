import { createServer } from 'node:http';
import { createReadStream } from 'node:fs';
import { stat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, extname, sep } from 'node:path';
import { createQoderMiddleware } from './qoder.mjs';

// Node's environment loader is optional: deployment can supply variables itself.
for (const path of ['.env.local', '.env']) {
  try { process.loadEnvFile?.(path); } catch (error) { if (error.code !== 'ENOENT') throw error; }
}

const root = fileURLToPath(new URL('../dist/', import.meta.url));
const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.webp': 'image/webp', '.ico': 'image/x-icon', '.woff2': 'font/woff2' };
const api = createQoderMiddleware();

async function serveStatic(req, res) {
  if (!['GET', 'HEAD'].includes(req.method)) { res.writeHead(405); return res.end(); }
  try {
    const pathname = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
    if (pathname.startsWith('/api/')) { res.writeHead(404); return res.end(); }
    let path = resolve(root, `.${pathname}`);
    if ((path !== resolve(root) && !path.startsWith(root.endsWith(sep) ? root : root + sep)) || pathname.includes('\0')) { res.writeHead(403); return res.end(); }
    let info;
    try { info = await stat(path); } catch { /* SPA routes use index.html below. */ }
    if (!info?.isFile()) {
      if (extname(pathname)) { res.writeHead(404); return res.end(); }
      path = resolve(root, 'index.html');
      info = await stat(path);
    }
    res.writeHead(200, {
      'Content-Type': types[extname(path)] || 'application/octet-stream',
      'Content-Length': info.size,
      'Cache-Control': extname(path) === '.html' ? 'no-cache' : 'public, max-age=3600',
      'X-Content-Type-Options': 'nosniff',
    });
    if (req.method === 'HEAD') return res.end();
    const stream = createReadStream(path);
    stream.on('error', () => res.destroy());
    stream.pipe(res);
  } catch {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
    res.end('页面不存在。请先运行 npm run build。');
  }
}

const host = process.env.HOST || '127.0.0.1';
const port = Number(process.env.PORT || 4173);
const server = createServer((req, res) => api(req, res, () => serveStatic(req, res)));
server.requestTimeout = 30_000;
server.listen(port, host, () => console.log(`TieTie: http://${host}:${port}`));
