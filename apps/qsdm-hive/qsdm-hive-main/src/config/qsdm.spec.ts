/**
 * @jest-environment node
 */

import {
  buildQsdmTaskActionReadUrls,
  getQsdmCoreConnectionMode,
  resolveQsdmCoreApiUrl,
  resolveQsdmTaskActionCoreApiUrl,
} from './qsdm';

const gateway = 'https://api.qsdm.tech/attest/home-validator/api/v1';
const canonical = 'https://api.qsdm.tech/api/v1';

describe('QSDM Core endpoint selection', () => {
  it('uses the canonical public Core by default on Linux', () => {
    expect(
      resolveQsdmCoreApiUrl({
        platform: 'linux',
        nodeEnv: 'production',
        canonicalUrl: canonical,
      })
    ).toBe(canonical);
  });

  it('retains the local Core default on Windows', () => {
    expect(
      resolveQsdmCoreApiUrl({
        platform: 'win32',
        nodeEnv: 'production',
        canonicalUrl: canonical,
      })
    ).toBe('http://127.0.0.1:8080/api/v1');
  });

  it('honors an explicit local Core override on Linux', () => {
    expect(
      resolveQsdmCoreApiUrl({
        platform: 'linux',
        nodeEnv: 'production',
        configuredUrl: 'http://localhost:8080/api/v1/',
        canonicalUrl: canonical,
      })
    ).toBe('http://127.0.0.1:8080/api/v1');
  });

  it('classifies local, gateway, and custom endpoints', () => {
    expect(
      getQsdmCoreConnectionMode('http://127.0.0.1:8080/api/v1', gateway)
    ).toBe('local');
    expect(getQsdmCoreConnectionMode(gateway, gateway)).toBe('gateway');
    expect(getQsdmCoreConnectionMode(canonical, gateway, canonical)).toBe(
      'gateway'
    );
    expect(
      getQsdmCoreConnectionMode('https://example.invalid/api/v1', gateway)
    ).toBe('custom');
    expect(
      getQsdmCoreConnectionMode(
        'https://api.qsdm.tech.evil.example/attest/home-validator/api/v1',
        gateway
      )
    ).toBe('custom');
  });

  it('confirms signed task actions against the main Core before a stale gateway', () => {
    expect(
      buildQsdmTaskActionReadUrls('/tasks/task-1/state', {
        runtimeApiUrl: gateway,
        taskRpcApiUrl: gateway,
        canonicalApiUrl: canonical,
      })
    ).toEqual([
      `${canonical}/tasks/task-1/state`,
      `${gateway}/tasks/task-1/state`,
    ]);
  });

  it('uses an explicitly configured custom Core for both writes and confirmation', () => {
    const custom = 'https://operator.example/api/v1';

    expect(
      resolveQsdmTaskActionCoreApiUrl({
        runtimeApiUrl: custom,
        canonicalApiUrl: canonical,
      })
    ).toBe(custom);
    expect(
      buildQsdmTaskActionReadUrls('/tasks/task-1/state', {
        runtimeApiUrl: custom,
        taskRpcApiUrl: gateway,
        canonicalApiUrl: canonical,
      })
    ).toEqual([
      `${custom}/tasks/task-1/state`,
      `${gateway}/tasks/task-1/state`,
      `${canonical}/tasks/task-1/state`,
    ]);
  });
});
