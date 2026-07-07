import { normalizeQsdmTaskType } from './qsdmTaskType';

describe('normalizeQsdmTaskType', () => {
  it.each([undefined, null, '', 'CELL', 'KOII', 'koii'])(
    'maps a native task type of %p without a token mint to CELL',
    (taskType) => {
      expect(normalizeQsdmTaskType({ taskType })).toBe('CELL');
    }
  );

  it('keeps an explicit KPL task as KPL', () => {
    expect(normalizeQsdmTaskType({ taskType: 'kpl' })).toBe('KPL');
  });

  it('uses a token mint as the authoritative KPL signal', () => {
    expect(
      normalizeQsdmTaskType({
        taskType: 'KOII',
        tokenType: 'example-token-mint',
      })
    ).toBe('KPL');
  });
});
