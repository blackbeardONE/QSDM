/**
 * @jest-environment node
 */
import { get } from 'http';

import express from 'express';

import { SERVER_HOST } from '../../../config/node';

import listenOnLoopback from './listenOnLoopback';

import type { AddressInfo } from 'net';

describe('listenOnLoopback', () => {
  it('binds the legacy task server to 127.0.0.1 only', async () => {
    expect(SERVER_HOST).toBe('127.0.0.1');

    const app = express();
    app.get('/id', (_req, res) => {
      res.send('ok');
    });

    const server = await new Promise<ReturnType<typeof listenOnLoopback>>(
      (resolve) => {
        const s = listenOnLoopback(app, 0, () => resolve(s));
      }
    );
    try {
      const address = server.address() as AddressInfo;
      expect(address.address).toBe('127.0.0.1');
      expect(address.port).toBeGreaterThan(0);

      const body = await new Promise<string>((resolve, reject) => {
        get(`http://127.0.0.1:${address.port}/id`, (response) => {
          let text = '';
          response.on('data', (chunk) => {
            text += chunk;
          });
          response.on('end', () => resolve(text));
        }).on('error', reject);
      });
      expect(body).toBe('ok');
    } finally {
      await new Promise<void>((resolve) => {
        server.close(() => resolve());
      });
    }
  });

  it('passes the loopback host to listen()', () => {
    const listen = jest.fn().mockReturnValue({});
    const onListening = jest.fn();

    listenOnLoopback({ listen }, 30017, onListening);

    expect(listen).toHaveBeenCalledWith(30017, '127.0.0.1', onListening);
  });
});
