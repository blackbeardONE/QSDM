import { SystemDbKeys } from 'config/systemDbKeys';
import { namespaceInstance } from 'main/node/helpers/Namespace';

import { getAllTimeRewards } from './getAllTimeRewards';

jest.mock('main/node/helpers/Namespace', () => ({
  namespaceInstance: {
    storeGet: jest.fn(),
  },
}));

describe('getAllTimeRewards', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (namespaceInstance.storeGet as jest.Mock).mockReturnValue(
      JSON.stringify({ exampleId: 1000 })
    );
  });

  it('returns all time rewards', async () => {
    const result = await getAllTimeRewards();

    expect(namespaceInstance.storeGet).toHaveBeenCalledWith(
      SystemDbKeys.AllTimeRewards
    );
    expect(result).toEqual({
      exampleId: 1000,
    });
  });

  it('returns an empty rewards cache when no rewards were stored yet', async () => {
    (namespaceInstance.storeGet as jest.Mock).mockReturnValue(undefined);

    const result = await getAllTimeRewards();

    expect(result).toEqual({});
  });

  it('returns an empty rewards cache when persisted rewards are malformed', async () => {
    (namespaceInstance.storeGet as jest.Mock).mockReturnValue('not-json');

    const result = await getAllTimeRewards();

    expect(result).toEqual({});
  });
});
