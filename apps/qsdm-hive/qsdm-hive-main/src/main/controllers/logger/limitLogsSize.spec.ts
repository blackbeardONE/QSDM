import { setMaxLogSize } from 'main/logger';

import getUserConfig from '../getUserConfig';
import storeUserConfig from '../storeUserConfig';

import { limitLogsSize } from './limitLogsSize';

jest.mock('../getUserConfig', () => ({
  __esModule: true,
  default: jest.fn(),
}));

jest.mock('../storeUserConfig', () => ({
  __esModule: true,
  default: jest.fn(),
}));

jest.mock('main/logger', () => ({
  setMaxLogSize: jest.fn(),
}));

describe('limitLogsSize', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('enables the 5 MB logger limit when the setting is off', async () => {
    (getUserConfig as jest.Mock).mockResolvedValue({ limitLogsSize: false });
    (storeUserConfig as jest.Mock).mockResolvedValue(true);

    await limitLogsSize();

    expect(storeUserConfig).toHaveBeenCalledWith(
      {},
      { settings: { limitLogsSize: true } }
    );
    expect(setMaxLogSize).toHaveBeenCalledWith(5);
  });

  it('removes the logger limit when the setting is on', async () => {
    (getUserConfig as jest.Mock).mockResolvedValue({ limitLogsSize: true });
    (storeUserConfig as jest.Mock).mockResolvedValue(true);

    await limitLogsSize();

    expect(storeUserConfig).toHaveBeenCalledWith(
      {},
      { settings: { limitLogsSize: false } }
    );
    expect(setMaxLogSize).toHaveBeenCalledWith(null);
  });
});
