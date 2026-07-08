import { AsyncSingleFlight } from './asyncSingleFlight';

describe('AsyncSingleFlight', () => {
  it('shares one operation across concurrent callers and resets afterward', async () => {
    let resolveOperation: (value: string) => void = () => undefined;
    const operation = jest.fn(
      () =>
        new Promise<string>((resolve) => {
          resolveOperation = resolve;
        })
    );
    const singleFlight = new AsyncSingleFlight<string>();

    const first = singleFlight.run(operation);
    const second = singleFlight.run(operation);

    expect(singleFlight.isRunning).toBe(true);
    expect(operation).toHaveBeenCalledTimes(1);
    resolveOperation('started');
    await expect(Promise.all([first, second])).resolves.toEqual([
      'started',
      'started',
    ]);
    expect(singleFlight.isRunning).toBe(false);

    await expect(singleFlight.run(async () => 'restarted')).resolves.toBe(
      'restarted'
    );
  });

  it('resets after a failed operation', async () => {
    const singleFlight = new AsyncSingleFlight<string>();

    await expect(
      singleFlight.run(async () => {
        throw new Error('failed');
      })
    ).rejects.toThrow('failed');

    expect(singleFlight.isRunning).toBe(false);
    await expect(singleFlight.run(async () => 'retry')).resolves.toBe('retry');
  });
});
