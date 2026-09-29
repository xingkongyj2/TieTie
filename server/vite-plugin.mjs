import { createQoderMiddleware } from './qoder.mjs';

/** The token stays in Node: do not expose it through Vite define or VITE_ vars. */
export function qoderPlugin(env) {
  return {
    name: 'tietie-qoder-api',
    configureServer(server) { server.middlewares.use(createQoderMiddleware({ env })); },
    configurePreviewServer(server) { server.middlewares.use(createQoderMiddleware({ env })); },
  };
}
