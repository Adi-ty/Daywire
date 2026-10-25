import path from 'node:path';

// Editions are read with fs outside Vite's module graph. Restarting dev clears
// both the module snapshot and Astro's cached getStaticPaths results.
export function watchEditions(directory) {
  return {
    name: 'daywire-watch-editions',
    apply: 'serve',
    configureServer(server) {
      let timer;
      const onEvent = (event, file) => {
        if (!['add', 'change', 'unlink'].includes(event)) return;
        const relative = path.relative(directory, path.resolve(file));
        if (!/^\d{4}-\d{2}-\d{2}\.json$/.test(relative)) return;
        clearTimeout(timer);
        // Coalesce atomic writes and archive restores into one restart.
        timer = setTimeout(() => {
          server.restart().catch(error => server.config.logger.error(String(error)));
        }, 150);
      };
      server.watcher.add(directory);
      server.watcher.on('all', onEvent);
      server.httpServer?.once('close', () => {
        clearTimeout(timer);
        server.watcher.off('all', onEvent);
      });
    },
  };
}
