import { renderHook, waitFor } from '@testing-library/react';
import React, { PropsWithChildren } from 'react';
import { QueryClient, QueryClientProvider } from 'react-query';

import { useAccountBalance } from 'renderer/features/settings/hooks/useAccountBalance';
import { useMainAccount } from 'renderer/features/settings/hooks/useMainAccount';
import { getQsdmCellAccount } from 'renderer/services';

import {
  qsdmCellBalanceToBaseUnits,
  useTaskWalletBalance,
} from './useTaskWalletBalance';

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
}));
jest.mock('renderer/features/settings/hooks/useAccountBalance');
jest.mock('renderer/features/settings/hooks/useMainAccount');
jest.mock('renderer/services', () => ({
  QueryKeys: {
    QsdmCellAccount: 'QsdmCellAccount',
  },
  getQsdmCellAccount: jest.fn(),
}));

const mockedUseAccountBalance = useAccountBalance as jest.Mock;
const mockedUseMainAccount = useMainAccount as jest.Mock;
const mockedGetQsdmCellAccount = getQsdmCellAccount as jest.Mock;

describe('useTaskWalletBalance', () => {
  beforeEach(() => {
    mockedUseMainAccount.mockReturnValue({ data: undefined });
    mockedUseAccountBalance.mockReturnValue({
      accountBalance: undefined,
      loadingAccountBalance: false,
      accountBalanceLoadingError: undefined,
    });
    mockedGetQsdmCellAccount.mockResolvedValue({
      configured: true,
      reachable: true,
      address: 'active-linux-signer',
      balance: 12.5,
    });
  });

  afterEach(() => {
    jest.resetAllMocks();
  });

  it('converts CELL into the base units used by task stake checks', () => {
    expect(qsdmCellBalanceToBaseUnits(12.5)).toBe(12_500_000_000);
    expect(qsdmCellBalanceToBaseUnits(undefined)).toBeUndefined();
  });

  it('uses the active QSDM signer even when no legacy main account exists', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    const { result } = renderHook(() => useTaskWalletBalance(), { wrapper });

    await waitFor(() => {
      expect(result.current.accountBalance).toBe(12_500_000_000);
    });
    expect(result.current.walletAddress).toBe('active-linux-signer');
    expect(result.current.accountBalanceLoadingError).toBeUndefined();
    expect(mockedUseAccountBalance).toHaveBeenCalledWith(undefined);
  });
});
