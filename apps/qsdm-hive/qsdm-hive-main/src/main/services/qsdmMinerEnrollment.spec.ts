import {
  buildQsdmMinerNodeId,
  inferQsdmNvidiaArchitecture,
  parseQsdmNvidiaSmiCsv,
  resolveQsdmMinerEnrollmentSubmitApiUrl,
} from './qsdmMinerEnrollment';

describe('qsdmMinerEnrollment', () => {
  it.each([
    ['7.5', 'turing'],
    ['8.6', 'ampere'],
    ['8.9', 'ada-lovelace'],
    ['9.0', 'hopper'],
    ['10.0', 'blackwell'],
  ])('maps compute capability %s to %s', (computeCapability, expected) => {
    expect(inferQsdmNvidiaArchitecture(computeCapability)).toBe(expected);
  });

  it('parses the first NVIDIA GPU without inventing identity fields', () => {
    const gpu = parseQsdmNvidiaSmiCsv(
      'GPU-abc, NVIDIA GeForce RTX 3050, 8.6, 572.16\nGPU-def, NVIDIA RTX 4090, 8.9, 572.16',
      '12.8'
    );

    expect(gpu).toEqual({
      uuid: 'GPU-abc',
      name: 'NVIDIA GeForce RTX 3050',
      computeCapability: '8.6',
      driverVersion: '572.16',
      cudaVersion: '12.8',
      architecture: 'ampere',
    });
  });

  it('rejects pre-Turing GPUs', () => {
    expect(() =>
      parseQsdmNvidiaSmiCsv('GPU-old, NVIDIA GTX 1080, 6.1, 535.1')
    ).toThrow(/Turing or newer/);
  });

  it('binds generated NodeIDs to the active signer wallet', () => {
    const first = buildQsdmMinerNodeId(
      'DESKTOP-QSDM',
      'GPU-abc',
      'a'.repeat(64)
    );
    const second = buildQsdmMinerNodeId(
      'DESKTOP-QSDM',
      'GPU-abc',
      'b'.repeat(64)
    );

    expect(first).toMatch(/^hive-desktop-qsdm-[0-9a-f]{12}$/);
    expect(second).not.toBe(first);
    expect(first.length).toBeLessThanOrEqual(64);
  });

  it('submits official-gateway enrollments to canonical Core', () => {
    expect(
      resolveQsdmMinerEnrollmentSubmitApiUrl({
        runtimeApiUrl: 'https://api.qsdm.tech/attest/home-validator/api/v1',
        gatewayApiUrl: 'https://api.qsdm.tech/attest/home-validator/api/v1',
        canonicalApiUrl: 'https://api.qsdm.tech/api/v1/',
      })
    ).toBe('https://api.qsdm.tech/api/v1');
  });

  it.each([
    'http://127.0.0.1:8080/api/v1/',
    'https://validator.example/api/v1/',
  ])('keeps non-gateway enrollment on %s', (runtimeApiUrl) => {
    expect(
      resolveQsdmMinerEnrollmentSubmitApiUrl({
        runtimeApiUrl,
        gatewayApiUrl: 'https://api.qsdm.tech/attest/home-validator/api/v1',
        canonicalApiUrl: 'https://api.qsdm.tech/api/v1',
      })
    ).toBe(runtimeApiUrl.replace(/\/+$/, ''));
  });
});
