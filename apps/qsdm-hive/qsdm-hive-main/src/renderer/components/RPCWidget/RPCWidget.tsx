import { Icon, InformationCircleLine } from 'vendor/qsdm-styleguide';
import React from 'react';
import { useMutation, useQuery } from 'react-query';

import type { QsdmCoreStatusResponse } from 'models/api/qsdm';
import {
  getQsdmCellAccount,
  getQsdmCoreStatus,
  openBrowserWindow,
  QueryKeys,
  runQsdmSignedCellLoop,
} from 'renderer/services';
import { useTheme } from 'renderer/theme/ThemeContext';
import { Theme } from 'renderer/types/common';

import { Popover } from '../ui/Popover/Popover';

const QSDM_CORE_REFETCH_INTERVAL = 30 * 1000;

const getQsdmCoreStatusLabel = (healthy?: boolean) => {
  if (healthy === undefined) return 'Checking';
  return healthy ? 'Online' : 'Offline';
};

const getQsdmCoreStatusColor = (
  healthy: boolean | undefined,
  isVip: boolean
) => {
  if (healthy === undefined) return 'text-gray-500';
  if (!healthy) return 'text-red-500';
  return isVip ? 'text-white vip-drop-shadow' : 'text-[#5ED9D1]';
};

const asRecord = (value: unknown): Record<string, unknown> | undefined => {
  if (!value || typeof value !== 'object') return undefined;
  return value as Record<string, unknown>;
};

const getNumericStatusValue = (
  status: QsdmCoreStatusResponse | undefined,
  keys: string[],
  scope: 'core' | 'taskRpc' = 'core'
) => {
  const statusRecord = asRecord(
    scope === 'taskRpc' ? status?.taskRpcStatus : status?.status
  );
  const healthRecord = asRecord(
    scope === 'taskRpc' ? status?.taskRpcHealth : status?.health
  );
  for (const key of keys) {
    const value = statusRecord?.[key] ?? healthRecord?.[key];
    if (typeof value === 'number' || typeof value === 'string') {
      return value;
    }
  }
  return undefined;
};

const getQsdmTaskRpcLabel = (status?: QsdmCoreStatusResponse) => {
  if (!status) return 'Checking';
  if (!status.taskRpcHealthy) return 'Offline';
  return 'OK';
};

const getQsdmChainHeightLabel = (status?: QsdmCoreStatusResponse) => {
  if (!status) return 'Checking';
  const chainTip =
    getNumericStatusValue(status, ['chain_tip', 'height', 'latest_height']) ??
    getNumericStatusValue(
      status,
      ['chain_tip', 'height', 'latest_height'],
      'taskRpc'
    );
  return chainTip !== undefined ? `${chainTip}` : 'Unknown';
};

const getQsdmTaskRpcColor = (
  status: QsdmCoreStatusResponse | undefined,
  isVip: boolean
) => {
  if (!status) return 'text-gray-500';
  if (!status.taskRpcHealthy) return 'text-red-500';
  return isVip ? 'text-white vip-drop-shadow' : 'text-[#5ED9D1]';
};

const formatCellBalance = (balance?: number, symbol?: string) => {
  if (balance === undefined) return null;
  return `${new Intl.NumberFormat('en-US', {
    maximumFractionDigits: 4,
  }).format(balance)} ${symbol || 'CELL'}`;
};

const getQsdmSignerLabel = (ready?: boolean) => {
  if (ready === undefined) return 'Checking';
  return ready ? 'Ready' : 'Setup';
};

const getQsdmSignerColor = (ready: boolean | undefined, isVip: boolean) => {
  if (ready === undefined) return 'text-gray-500';
  if (!ready) return 'text-yellow-500';
  return isVip ? 'text-white vip-drop-shadow' : 'text-[#5ED9D1]';
};

const getSignedLoopButtonLabel = ({
  isLoading,
  isError,
  isSuccess,
}: {
  isLoading: boolean;
  isError: boolean;
  isSuccess: boolean;
}) => {
  if (isLoading) return 'Proof...';
  if (isError) return 'Proof failed';
  if (isSuccess) return 'Proof OK';
  return 'Proof';
};

export function RPCWidget() {
  const { theme } = useTheme();
  const isVip = !!(theme === 'vip');

  const { data: qsdmCoreStatus } = useQuery(
    QueryKeys.QsdmCoreStatus,
    getQsdmCoreStatus,
    { refetchInterval: QSDM_CORE_REFETCH_INTERVAL, retry: false }
  );

  const { data: qsdmCellAccount, refetch: refetchQsdmCellAccount } = useQuery(
    QueryKeys.QsdmCellAccount,
    () => getQsdmCellAccount(),
    { refetchInterval: QSDM_CORE_REFETCH_INTERVAL, retry: false }
  );

  const signedCellLoop = useMutation(() => runQsdmSignedCellLoop(), {
    onSuccess: () => {
      refetchQsdmCellAccount();
    },
    onError: (error) => {
      console.error('QSDM signed CELL proof loop failed', error);
    },
  });

  const qsdmTaskRpcLabel = getQsdmTaskRpcLabel(qsdmCoreStatus);
  const qsdmTaskRpcColor = getQsdmTaskRpcColor(qsdmCoreStatus, isVip);
  const qsdmChainHeightLabel = getQsdmChainHeightLabel(qsdmCoreStatus);
  const qsdmCoreStatusLabel = getQsdmCoreStatusLabel(qsdmCoreStatus?.healthy);
  const qsdmCoreStatusColor = getQsdmCoreStatusColor(
    qsdmCoreStatus?.healthy,
    isVip
  );
  const qsdmCellBalanceLabel = qsdmCellAccount?.configured
    ? formatCellBalance(qsdmCellAccount.balance, qsdmCellAccount.tokenSymbol)
    : null;
  const qsdmSignerReady = qsdmCoreStatus?.taskSigner?.ready;
  const qsdmSignerLabel = getQsdmSignerLabel(qsdmSignerReady);
  const qsdmSignerColor = getQsdmSignerColor(qsdmSignerReady, isVip);
  const canRunSignedLoop =
    !!qsdmCoreStatus?.healthy &&
    !!qsdmCoreStatus?.taskSigner?.localLoopEnabled &&
    !!qsdmCoreStatus?.taskSigner?.ready &&
    !signedCellLoop.isLoading;

  const getSignedLoopTitle = () => {
    if (!qsdmCoreStatus?.healthy) return 'QSDM Core must be online';
    if (!qsdmCoreStatus.taskSigner?.localLoopEnabled) {
      return 'Enable QSDM_ENABLE_LOCAL_SIGNED_LOOP=1 to run CELL proof actions';
    }
    return qsdmCoreStatus.taskSigner?.reason || 'Run CELL proof loop';
  };

  const openStatusPage = async () => {
    await openBrowserWindow(
      qsdmCoreStatus?.dashboardUrl || 'http://localhost:8081'
    );
  };

  return (
    <div className="flex flex-row items-center gap-2 px-3 py-1.5 rounded-full bg-[#BEF0ED]/10 whitespace-nowrap">
      <Popover
        tooltipContent="Open QSDM Core dashboard"
        theme={Theme.Dark}
        asChild
      >
        <button
          className="text-white/80 hover:text-white hover:brightness-125 hover:scale-110 hover:rotate-[361deg] transition-all duration-300 ease-in-out font-medium text-sm flex items-center gap-2"
          onClick={openStatusPage}
        >
          <Icon source={InformationCircleLine} className="w-5 h-5" />
        </button>
      </Popover>
      <span className="hidden xl:inline text-white/80">Task RPC</span>
      <span className={`text-sm font-medium ${qsdmTaskRpcColor}`}>
        {qsdmTaskRpcLabel}
      </span>
      <span className="h-4 w-px bg-white/20" aria-hidden="true" />
      <span className="hidden xl:inline text-white/80">Chain Height</span>
      <span className="text-sm font-medium text-white">
        {qsdmChainHeightLabel}
      </span>
      <span className="h-4 w-px bg-white/20" aria-hidden="true" />
      <span className="hidden xl:inline text-white/80">QSDM Core</span>
      <span className={`text-sm font-medium ${qsdmCoreStatusColor}`}>
        {qsdmCoreStatusLabel}
      </span>
      {qsdmCellBalanceLabel && (
        <>
          <span className="h-4 w-px bg-white/20" aria-hidden="true" />
          <span className="text-white/80">Wallet Balance:</span>
          <span className="text-sm font-medium text-white">
            {qsdmCellBalanceLabel}
          </span>
        </>
      )}
      <span className="h-4 w-px bg-white/20" aria-hidden="true" />
      <span className="hidden 2xl:inline text-white/80">Signer</span>
      <span className={`text-sm font-medium ${qsdmSignerColor}`}>
        {qsdmSignerLabel}
      </span>
      {qsdmCoreStatus?.taskSigner && (
        <button
          className="text-xs font-semibold px-2 py-1 rounded-full border border-white/20 text-white/90 hover:border-[#5ED9D1] hover:text-[#5ED9D1] disabled:opacity-40 disabled:hover:text-white/90 disabled:hover:border-white/20"
          disabled={!canRunSignedLoop}
          onClick={() => signedCellLoop.mutate()}
          title={getSignedLoopTitle()}
        >
          {getSignedLoopButtonLabel(signedCellLoop)}
        </button>
      )}
    </div>
  );
}
