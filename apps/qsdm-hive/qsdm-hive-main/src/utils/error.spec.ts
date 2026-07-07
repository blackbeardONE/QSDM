import { ErrorType } from 'models';

import { throwDetailedError } from './error';

describe('throwDetailedError', () => {
  it('preserves an Error message across Electron serialization', () => {
    expect.assertions(2);

    try {
      throwDetailedError({
        detailed: new Error('packaged miner is missing') as unknown as string,
        type: ErrorType.TASK_START,
      });
    } catch (error) {
      const serialized = (error as Error).message;
      const parsed = JSON.parse(serialized) as {
        type: ErrorType;
        detailed: string;
      };

      expect(parsed.type).toBe(ErrorType.TASK_START);
      expect(parsed.detailed).toBe('packaged miner is missing');
    }
  });
});
