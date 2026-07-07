import { RawTaskData } from 'models';

import { parseRawK2TaskData } from './parseRawK2TaskData';

const legacyTask = (overrides: Record<string, unknown> = {}) =>
  ({
    task_id: 'legacy-native-task',
    task_name: 'Legacy Native Task',
    task_type: 'KOII',
    token_type: null,
    ...overrides,
  } as unknown as RawTaskData);

describe('parseRawK2TaskData QSDM task type compatibility', () => {
  it('parses a legacy KOII task without a token mint as CELL', () => {
    expect(parseRawK2TaskData({ rawTaskData: legacyTask() }).taskType).toBe(
      'CELL'
    );
  });

  it('parses a legacy KOII task with a token mint as KPL', () => {
    expect(
      parseRawK2TaskData({
        rawTaskData: legacyTask({ token_type: 'example-token-mint' }),
      }).taskType
    ).toBe('KPL');
  });
});
