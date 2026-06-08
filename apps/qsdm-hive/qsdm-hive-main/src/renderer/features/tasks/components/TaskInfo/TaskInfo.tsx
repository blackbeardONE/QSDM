import React, { RefObject, useMemo, useState } from 'react';
import { toast } from 'react-hot-toast';
import { useMutation, useQuery, useQueryClient } from 'react-query';

import {
  isQsdmMinerSystemTaskId,
  isQsdmSkyFangLinkSystemTaskId,
} from 'config/qsdmSystemTasks';
import {
  QsdmSkyFangLinkResponse,
  QsdmSkyFangLinkStatusResponse,
} from 'models/api/qsdm';
import { TaskPairing } from 'models';
import { TaskMetadata } from 'models/task';
import { useAverageSlotTime } from 'renderer/features/common';
import {
  getQsdmSkyFangLinkStatus,
  getQsdmMinerRewardStatus,
  linkQsdmSkyFangAccount,
  openBrowserWindow,
  QueryKeys,
  setQsdmMinerRewardAddressToSigner,
} from 'renderer/services';
import { formatRoundTimeWithFullUnit, getErrorToDisplay } from 'renderer/utils';

import { useTaskRoundTime } from '../../hooks/useRoundTime';
import { formatNumber } from '../../utils';

import { TaskActions } from './components/TaskActions';
import { TaskDescription } from './components/TaskDescription';
import { TaskStats } from './components/TaskStats';
import { UpgradeInfo } from './components/UpgradeInfo';
import { Setting } from './Setting';

const shortAddress = (address?: string) => {
  if (!address) return '-';
  if (address.length <= 16) return address;
  return `${address.slice(0, 8)}...${address.slice(-8)}`;
};

const formatCell = (value?: number) => {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '-';
  return `${formatNumber(value, false)} CELL`;
};

const SKYFANG_ACCOUNT_CACHE_PREFIX = 'qsdm.skyfang.link.account.';

const getSkyFangAccountName = (
  value?: Partial<QsdmSkyFangLinkResponse & QsdmSkyFangLinkStatusResponse>
) => value?.username || value?.account || value?.player || '';

const readCachedSkyFangAccount = (address?: string) => {
  if (!address) return '';
  try {
    return (
      window.localStorage.getItem(
        `${SKYFANG_ACCOUNT_CACHE_PREFIX}${address.toLowerCase()}`
      ) || ''
    );
  } catch {
    return '';
  }
};

const cacheSkyFangAccount = (address?: string, account?: string) => {
  if (!address || !account) return;
  try {
    window.localStorage.setItem(
      `${SKYFANG_ACCOUNT_CACHE_PREFIX}${address.toLowerCase()}`,
      account
    );
  } catch {
    // Local display cache is optional; live link status remains authoritative.
  }
};

export type TaskStatsDataType = {
  nodesNumber: number;
  minStake: number;
  myStake?: number;
  topStake: number;
  bounty: number;
  totalStakeInCell?: number;
  roundTime?: number;
};

type PropsType = {
  publicKey: string;
  creator: string;
  metadataCID: string;
  metadata?: TaskMetadata;
  details: TaskStatsDataType;
  variables?: TaskPairing[];
  shouldDisplayToolsInUse?: boolean;
  showSourceCode?: boolean;
  isRunning?: boolean;
  isUpgradeInfo?: boolean;
  onOpenAddTaskVariableModal?: (
    dropdownRef: RefObject<HTMLButtonElement>,
    settingName: string
  ) => void;
  shouldDisplayArchiveButton?: boolean;
  isOnboardingTask?: boolean;
  tokenTicker?: string;
};

function SkyFangLinkPanel({ publicKey }: { publicKey: string }) {
  const queryClient = useQueryClient();
  const [skyFangLinkCode, setSkyFangLinkCode] = useState('');
  const [cachedSkyFangAccount, setCachedSkyFangAccount] = useState('');
  const [isSkyFangLinkModalOpen, setIsSkyFangLinkModalOpen] = useState(false);
  const {
    data: skyFangLinkStatus,
    isLoading: isLoadingSkyFangLinkStatus,
  } = useQuery(
    [QueryKeys.QsdmSkyFangLinkStatus, publicKey],
    getQsdmSkyFangLinkStatus,
    {
      refetchInterval: 10000,
      staleTime: 5000,
      onSuccess: (status) => {
        setCachedSkyFangAccount(readCachedSkyFangAccount(status?.address));
      },
    }
  );
  const {
    mutate: linkSkyFangAccount,
    isLoading: isLinkingSkyFangAccount,
  } = useMutation(linkQsdmSkyFangAccount, {
    onSuccess: (result) => {
      const accountName = getSkyFangAccountName(result);
      cacheSkyFangAccount(result.address, accountName);
      setCachedSkyFangAccount(accountName);
      setSkyFangLinkCode('');
      setIsSkyFangLinkModalOpen(false);
      queryClient.invalidateQueries([QueryKeys.QsdmSkyFangLinkStatus]);
      queryClient.invalidateQueries([QueryKeys.TaskList]);
      queryClient.invalidateQueries([QueryKeys.myTaskList]);
      queryClient.invalidateQueries([QueryKeys.availableTaskList]);
      toast.success(
        accountName
          ? `Sky Fang account ${accountName} is linked.`
          : 'Sky Fang account is linked.'
      );
    },
    onError: (error: any) => {
      toast.error(
        getErrorToDisplay(error) || 'Unable to link the Sky Fang account.'
      );
    },
  });
  const skyFangAccountName =
    getSkyFangAccountName(skyFangLinkStatus) || cachedSkyFangAccount;

  const handleSkyFangLinkSubmit = (event: React.FormEvent) => {
    event.preventDefault();
    const code = skyFangLinkCode.trim();
    if (!code) {
      toast.error('Enter the Sky Fang link code first.');
      return;
    }
    linkSkyFangAccount({ code });
  };

  return (
    <div className="flex flex-col gap-3 rounded-lg bg-finnieBlue-light-transparent px-4 py-3 text-xs">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-wrap gap-x-8 gap-y-2">
          <div>
            <span className="text-finnieTeal-100">Sky Fang</span>
            <div
              className={
                skyFangLinkStatus?.linked
                  ? 'font-semibold text-finnieTeal-100'
                  : 'font-semibold text-finnieOrange'
              }
            >
              {isLoadingSkyFangLinkStatus
                ? 'Checking...'
                : skyFangLinkStatus?.linked
                ? 'Linked'
                : 'Not linked'}
            </div>
          </div>
          <div>
            <span className="text-finnieTeal-100">Wallet</span>
            <div className="select-text">
              {shortAddress(skyFangLinkStatus?.address)}
            </div>
          </div>
          <div>
            <span className="text-finnieTeal-100">Account</span>
            <div className="select-text">
              {skyFangAccountName ||
                (skyFangLinkStatus?.linked ? 'Linked' : '-')}
            </div>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {!skyFangLinkStatus?.linked && (
            <button
              type="button"
              className="rounded-md bg-finnieTeal-100 px-4 py-2 font-semibold text-finnieBlue-light-secondary transition hover:bg-finnieTeal"
              onClick={() => setIsSkyFangLinkModalOpen(true)}
            >
              Link account
            </button>
          )}
          <button
            type="button"
            className="rounded-md border border-finnieTeal-100/40 px-4 py-2 font-semibold text-finnieTeal-100 transition hover:border-finnieTeal-100"
            onClick={() =>
              openBrowserWindow('https://skyfang.xyz/login?next=/dashboard/qsdm')
            }
          >
            Open Sky Fang
          </button>
        </div>
      </div>

      {!skyFangLinkStatus?.linked && skyFangLinkStatus?.detail && (
        <div className="text-finnieOrange">{skyFangLinkStatus.detail}</div>
      )}

      {isSkyFangLinkModalOpen && (
        <div className="fixed inset-0 z-50 grid place-items-center bg-black/60 px-6">
          <div className="w-full max-w-[520px] rounded-lg border border-finnieTeal-100/30 bg-finnieBlue-light-secondary p-5 text-white shadow-2xl">
            <div className="mb-5 flex items-center justify-between gap-4">
              <h3 className="text-lg font-semibold">Link Sky Fang Account</h3>
              <button
                type="button"
                className="grid h-8 w-8 place-items-center rounded-md border border-white/20 text-lg leading-none transition hover:border-finnieTeal-100"
                aria-label="Close"
                onClick={() => setIsSkyFangLinkModalOpen(false)}
              >
                x
              </button>
            </div>

            <form
              className="flex flex-col gap-4"
              onSubmit={handleSkyFangLinkSubmit}
            >
              <label className="flex flex-col gap-1">
                <span className="text-finnieTeal-100">Link code</span>
                <input
                  className="rounded-md border border-finnieTeal-100/30 bg-finnieBlue px-3 py-2 text-white placeholder:text-white/50 outline-none transition focus:border-finnieTeal-100"
                  value={skyFangLinkCode}
                  placeholder="Paste Sky Fang code"
                  autoFocus
                  autoCapitalize="none"
                  autoCorrect="off"
                  spellCheck={false}
                  onChange={(event) => setSkyFangLinkCode(event.target.value)}
                />
              </label>

              <div className="flex flex-wrap justify-end gap-2">
                <button
                  type="button"
                  className="rounded-md border border-finnieTeal-100/40 px-4 py-2 font-semibold text-finnieTeal-100 transition hover:border-finnieTeal-100"
                  onClick={() =>
                    openBrowserWindow(
                      'https://skyfang.xyz/login?next=/dashboard/qsdm'
                    )
                  }
                >
                  Open Sky Fang
                </button>
                <button
                  type="submit"
                  className="rounded-md bg-finnieTeal-100 px-4 py-2 font-semibold text-finnieBlue-light-secondary transition hover:bg-finnieTeal disabled:cursor-not-allowed disabled:opacity-50"
                  disabled={isLinkingSkyFangAccount || !skyFangLinkCode.trim()}
                >
                  {isLinkingSkyFangAccount ? 'Linking...' : 'Link account'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}

export function TaskInfo({
  publicKey,
  metadata,
  details: {
    nodesNumber,
    myStake,
    topStake,
    bounty,
    totalStakeInCell,
    roundTime,
  },
  variables,
  shouldDisplayToolsInUse,
  showSourceCode = true,
  isRunning,
  isUpgradeInfo,
  onOpenAddTaskVariableModal,
  shouldDisplayArchiveButton,
  tokenTicker,
}: PropsType) {
  const queryClient = useQueryClient();
  const { data: averageSlotTime } = useAverageSlotTime();
  const isMinerTask = isQsdmMinerSystemTaskId(publicKey);
  const isSkyFangLinkTask = isQsdmSkyFangLinkSystemTaskId(publicKey);
  const { data: minerRewardStatus, isLoading: isLoadingMinerRewardStatus } =
    useQuery(
      [QueryKeys.QsdmMinerRewardStatus, publicKey],
      getQsdmMinerRewardStatus,
      {
        enabled: isMinerTask,
        refetchInterval: isMinerTask ? 10000 : false,
        staleTime: isMinerTask ? 5000 : Infinity,
      }
    );
  const {
    mutate: alignMinerRewardAddress,
    isLoading: isAligningMinerRewardAddress,
  } = useMutation(setQsdmMinerRewardAddressToSigner, {
    onSuccess: (result) => {
      queryClient.invalidateQueries([QueryKeys.QsdmMinerRewardStatus]);
      toast.success(
        result.updated
          ? 'Miner reward address updated. Restart the miner task to apply it.'
          : 'Miner reward address already matches the Hive signer.'
      );
    },
    onError: (error: any) => {
      toast.error(error?.message || 'Unable to update miner reward address.');
    },
  });
  const parsedRoundTime = useTaskRoundTime({
    roundTimeInMs: roundTime || 0,
    averageSlotTime,
  });
  const fullRoundTime =
    parsedRoundTime &&
    formatRoundTimeWithFullUnit({ ...parsedRoundTime, useShortUnits: true });

  const taskStatistics = useMemo(
    () => [
      { label: 'Token', value: tokenTicker },
      ...(myStake !== undefined
        ? [
            {
              label: 'My Stake',
              value: `${formatNumber(myStake, false)}`,
              fullValue: myStake,
            },
          ]
        : []),
      {
        label: 'Bounty',
        value: `${formatNumber(bounty, false)}`,
        fullValue: bounty,
      },
      {
        label: 'Top Stake',
        value: `${formatNumber(topStake, false)}`,
        fullValue: topStake,
      },
      {
        label: 'Total Stake',
        value: totalStakeInCell ? formatNumber(totalStakeInCell, false) : 0,
        fullValue: totalStakeInCell,
      },
      { label: 'Nodes', value: nodesNumber },
      ...(roundTime !== undefined
        ? [
            {
              label: 'Round Time',
              value: fullRoundTime,
            },
          ]
        : []),
    ],
    [
      bounty,
      nodesNumber,
      topStake,
      totalStakeInCell,
      myStake,
      roundTime,
      fullRoundTime,
    ]
  );

  return (
    <div className="flex flex-col w-full gap-4 pl-3 pr-5 cursor-default">
      <UpgradeInfo
        isUpgradeInfo={isUpgradeInfo}
        migrationDescription={metadata?.migrationDescription}
      />

      <div className="flex justify-between">
        <div className="flex flex-col gap-8 max-w-[80%]">
          <TaskDescription
            description={metadata?.description}
            taskId={publicKey}
          />
        </div>

        <TaskActions
          publicKey={publicKey}
          showSourceCode={showSourceCode}
          repositoryUrl={metadata?.repositoryUrl}
          shouldDisplayArchiveButton={shouldDisplayArchiveButton}
          moreInfoLink={metadata?.infoUrl}
        />
      </div>

      {isMinerTask && (
        <div className="flex flex-wrap gap-x-6 gap-y-2 rounded-lg bg-finnieBlue-light-transparent px-4 py-3 text-xs">
          <div>
            <span className="text-finnieTeal-100">Reward source</span>
            <div>QSDM protocol mining emission</div>
          </div>
          <div>
            <span className="text-finnieTeal-100">Reward address</span>
            <div className="select-text">
              {isLoadingMinerRewardStatus
                ? 'checking...'
                : shortAddress(minerRewardStatus?.rewardAddress)}
            </div>
          </div>
          <div>
            <span className="text-finnieTeal-100">Mining balance</span>
            <div>{formatCell(minerRewardStatus?.balanceCell)}</div>
          </div>
          <div>
            <span className="text-finnieTeal-100">Earned in Hive</span>
            <div>{formatCell(minerRewardStatus?.earnedCell)}</div>
          </div>
          {minerRewardStatus?.warning && (
            <div className="basis-full flex flex-wrap items-center gap-3 text-finnieOrange">
              <span>{minerRewardStatus.warning}</span>
              {minerRewardStatus.signerAddress &&
                !minerRewardStatus.rewardAddressMatchesSigner && (
                  <button
                    type="button"
                    className="rounded-md bg-finnieTeal-100 px-3 py-1 text-finnieBlue-light-secondary transition hover:bg-finnieTeal"
                    disabled={isAligningMinerRewardAddress}
                    onClick={() => alignMinerRewardAddress()}
                  >
                    {isAligningMinerRewardAddress
                      ? 'Updating...'
                      : 'Use Hive signer'}
                  </button>
                )}
            </div>
          )}
          {minerRewardStatus?.error && (
            <div className="basis-full text-finnieOrange">
              Miner reward lookup failed: {minerRewardStatus.error}
            </div>
          )}
        </div>
      )}

      {isSkyFangLinkTask && <SkyFangLinkPanel publicKey={publicKey} />}

      <TaskStats taskStatistics={taskStatistics} />

      {/* <div className="flex justify-between w-full mb-6 text-start">
        <div className={taskSpecificationClass}>
          <div className="mb-2 text-base font-semibold">Specifications</div>
          <div className={gridClass}>
            {specs?.map(({ type, value }, index) => (
              <div key={index} className="select-text">
                {type}: {value ?? NOT_AVAILABLE_PLACEHOLDER}
              </div>
            ))}
          </div>
        </div>
      </div> */}

      {shouldDisplayToolsInUse && !!variables?.length && (
        <div>
          <div
            className="mb-2 text-base font-semibold"
            id={`task-settings-${publicKey}`}
          >
            Extensions
          </div>
          {/* Adjust the grid-cols class as needed for different breakpoints */}
          <div className="flex flex-wrap w-full gap-x-6">
            {variables?.map(({ name, label }) => (
              <Setting
                publicKey={publicKey}
                isRunning={isRunning}
                key={name}
                name={name}
                label={label}
                onOpenAddTaskVariableModal={onOpenAddTaskVariableModal}
                isEditDisabled={isUpgradeInfo}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
