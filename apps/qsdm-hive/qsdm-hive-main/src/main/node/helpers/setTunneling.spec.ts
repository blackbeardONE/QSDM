import startLocalTunnel from './setTunneling';

describe('startLocalTunnel', () => {
  it('fails closed without creating a third-party tunnel', async () => {
    const result = await startLocalTunnel();

    expect(result).toEqual({
      success: false,
      error:
        'Third-party localtunnel exposure is disabled. QSDM Core remains available through the configured home or canonical gateway.',
    });
    expect(result.tunnel).toBeUndefined();
  });
});
