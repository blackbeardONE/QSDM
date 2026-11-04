import { SERVER_HOST } from '../../../config/node';

import type { Server } from 'http';

interface Listenable {
  listen(port: number, hostname: string, callback?: () => void): Server;
}

/**
 * Starts the legacy task server on the loopback interface only. The
 * server has no authentication, so it must never listen on all
 * interfaces; Hive and the task processes it launches reach it through
 * 127.0.0.1.
 */
const listenOnLoopback = (
  app: Listenable,
  port: number,
  onListening?: () => void
): Server => app.listen(port, SERVER_HOST, onListening);

export default listenOnLoopback;
