import {
  faArrowRightFromBracket,
  faCircleCheck,
  faLink,
  faMemory,
  faMicrochip,
  faPlay,
  faRotate,
  faServer,
  faStop,
  faTriangleExclamation,
} from '@fortawesome/free-solid-svg-icons';
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome';
import React, { useMemo, useState } from 'react';
import { toast } from 'react-hot-toast';
import { useMutation, useQuery, useQueryClient } from 'react-query';

import {
  QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT,
  QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
} from 'config/qsdmSystemTasks';
import { QsdmMotherHiveStatusResponse } from 'models/api/qsdm';
import { Button, LoadingSpinner } from 'renderer/components/ui';
import { useMyTaskStake } from 'renderer/features/tasks/hooks/useMyTaskStake';
import { useStakeOnTask } from 'renderer/features/tasks/hooks/useStakeOnTask';
import {
  disconnectQsdmMotherHive,
  getIsTaskRunning,
  getQsdmMotherHiveStatus,
  getTasksById,
  pairQsdmMotherHive,
  QueryKeys,
  startTask,
  stopTask,
} from 'renderer/services';
import { getErrorToDisplay } from 'renderer/utils';
import { getCellFromBaseUnits } from 'utils';

const formatMemory = (valueMiB: number) =>
  valueMiB >= 1024
    ? `${(valueMiB / 1024).toFixed(valueMiB >= 10240 ? 0 : 1)} GiB`
    : `${Math.round(valueMiB)} MiB`;

function Metric({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
}) {
  return (
    <div className="min-w-0 border border-white/10 bg-finnieBlue-light-transparent p-4 rounded-md">
      <div className="flex items-center gap-2 text-finnieTeal-100 text-xs">
        {icon}
        <span>{label}</span>
      </div>
      <div className="mt-2 text-xl font-semibold break-words">{value}</div>
    </div>
  );
}

export function MotherHiveView() {
  const queryClient = useQueryClient();
  const [pairingCode, setPairingCode] = useState('');

  const statusQuery = useQuery<QsdmMotherHiveStatusResponse>(
    QueryKeys.QsdmMotherHiveStatus,
    getQsdmMotherHiveStatus,
    { refetchInterval: 10000, staleTime: 4000 }
  );
  const taskQuery = useQuery(
    [QueryKeys.TaskInfo, QSDM_MOTHER_HIVE_SYSTEM_TASK_ID],
    async () =>
      (await getTasksById([QSDM_MOTHER_HIVE_SYSTEM_TASK_ID]))[0] || null,
    { staleTime: 30000 }
  );
  const runningQuery = useQuery(
    [QueryKeys.IsRunning, QSDM_MOTHER_HIVE_SYSTEM_TASK_ID],
    () => getIsTaskRunning(QSDM_MOTHER_HIVE_SYSTEM_TASK_ID),
    { refetchInterval: 5000 }
  );
  const stakeQuery = useMyTaskStake(
    QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
    'CELL',
    true,
    true
  );
  const stakeMutation = useStakeOnTask({ skipIfItIsAlreadyStaked: true });

  const status = statusQuery.data;
  const task = taskQuery.data;
  const isRunning = Boolean(runningQuery.data);
  const currentStake = Number(stakeQuery.data) || 0;
  const minimumStake =
    task?.minimumStakeAmount || QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT;
  const missingStake = Math.max(0, minimumStake - currentStake);

  const refresh = async () => {
    await Promise.all([
      statusQuery.refetch(),
      taskQuery.refetch(),
      runningQuery.refetch(),
      stakeQuery.refetch(),
    ]);
  };

  const pairMutation = useMutation(
    () => pairQsdmMotherHive(pairingCode.trim()),
    {
      onSuccess: (nextStatus) => {
        queryClient.setQueryData(QueryKeys.QsdmMotherHiveStatus, nextStatus);
        setPairingCode('');
        toast.success(
          nextStatus.connected
            ? 'Relay paired. Agents are now visible.'
            : 'Relay pairing saved. Start the Relay to connect.'
        );
      },
      onError: (error: Error) => {
        toast.error(getErrorToDisplay(error) || 'Relay pairing failed.');
      },
    }
  );

  const disconnectMutation = useMutation(disconnectQsdmMotherHive, {
    onSuccess: (nextStatus) => {
      queryClient.setQueryData(QueryKeys.QsdmMotherHiveStatus, nextStatus);
      toast.success('Mother Hive disconnected from the Relay.');
    },
    onError: (error: Error) => {
      toast.error(
        getErrorToDisplay(error) || 'Could not disconnect the Relay.'
      );
    },
  });

  const taskMutation = useMutation(
    async () => {
      if (isRunning) {
        await stopTask(QSDM_MOTHER_HIVE_SYSTEM_TASK_ID);
        return false;
      }
      if (!status?.configured) {
        throw new Error('Pair a Relay before starting Mother Hive.');
      }
      if (!task) {
        throw new Error('Mother Hive task information is unavailable.');
      }
      if (missingStake > 0) {
        await stakeMutation.mutateAsync({
          taskAccountPubKey: QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
          stakeAmount: missingStake,
          stakePotAccount: task.stakePotAccount,
          taskType: 'CELL',
          isNetworkingTask: false,
        });
      }
      await startTask(QSDM_MOTHER_HIVE_SYSTEM_TASK_ID, false, true);
      return true;
    },
    {
      onSuccess: async (started) => {
        toast.success(
          started ? 'Mother Hive started.' : 'Mother Hive stopped.'
        );
        await refresh();
        queryClient.invalidateQueries([QueryKeys.TaskList]);
      },
      onError: (error: Error) => {
        toast.error(getErrorToDisplay(error) || 'Mother Hive task failed.');
      },
    }
  );

  const onlineWorkers = useMemo(
    () => status?.workers.filter((worker) => worker.online) || [],
    [status?.workers]
  );
  const aggressiveRelayPolicy = Boolean(
    status?.relayPolicy &&
      Math.max(
        status.relayPolicy.cpuPercent,
        status.relayPolicy.gpuPercent,
        status.relayPolicy.ramPercent
      ) >= 90
  );
  const busy =
    pairMutation.isLoading ||
    disconnectMutation.isLoading ||
    taskMutation.isLoading ||
    stakeMutation.isLoading;

  return (
    <div className="h-full overflow-y-auto pr-2 pb-12 text-white">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-white/20 pb-4">
        <div>
          <h1 className="text-2xl font-semibold">Mother Hive</h1>
          <p className="mt-1 text-sm text-white/65">
            Relay coordination and pooled QSDM resources
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div
            className={`flex items-center gap-2 rounded-md px-3 py-2 text-sm ${
              status?.connected
                ? 'bg-finnieEmerald/20 text-finnieTeal-100'
                : 'bg-finnieOrange/15 text-finnieOrange'
            }`}
          >
            <FontAwesomeIcon
              icon={status?.connected ? faCircleCheck : faTriangleExclamation}
            />
            {statusQuery.isLoading
              ? 'Checking Relay'
              : status?.connected
              ? 'Relay connected'
              : status?.configured
              ? 'Relay unavailable'
              : 'Relay not paired'}
          </div>
          <Button
            onlyIcon
            label="Refresh Mother Hive"
            title="Refresh Mother Hive"
            icon={<FontAwesomeIcon icon={faRotate} />}
            onClick={refresh}
            disabled={statusQuery.isFetching}
            className="h-10 w-10 rounded-md border border-white/20"
          />
        </div>
      </header>

      <section className="grid gap-4 border-b border-white/15 py-5 lg:grid-cols-[minmax(0,1fr)_auto]">
        <div className="min-w-0">
          <h2 className="font-semibold">Relay connection</h2>
          <div className="mt-3 flex min-w-0 flex-col gap-3 sm:flex-row">
            <input
              type="password"
              value={pairingCode}
              onChange={(event) => setPairingCode(event.target.value)}
              placeholder="Mother Hive pairing code"
              aria-label="Mother Hive pairing code"
              className="h-10 min-w-0 flex-1 rounded-md border border-white/15 bg-finnieBlue-light-tertiary px-3 text-sm outline-none focus:border-finnieTeal"
            />
            <Button
              label="Pair Relay"
              icon={<FontAwesomeIcon icon={faLink} />}
              onClick={() => pairMutation.mutate()}
              disabled={!pairingCode.trim() || busy}
              loading={pairMutation.isLoading}
              className="w-[150px] bg-finnieTeal text-finnieBlue-dark"
            />
          </div>
          <p className="mt-2 text-xs text-white/55">
            Paste the Mother Hive code from QSDM Edge Control on the Relay
            computer.
          </p>
          {status?.detail && (
            <p className="mt-2 text-sm text-white/70">{status.detail}</p>
          )}
        </div>
        <div className="flex items-end">
          <Button
            label="Disconnect"
            icon={<FontAwesomeIcon icon={faArrowRightFromBracket} />}
            onClick={() => disconnectMutation.mutate()}
            disabled={!status?.configured || busy}
            loading={disconnectMutation.isLoading}
            className="w-[150px] border border-white/20 bg-transparent"
          />
        </div>
      </section>

      <section className="flex flex-wrap items-center justify-between gap-4 border-b border-white/15 py-5">
        <div>
          <h2 className="font-semibold">Mother Hive task</h2>
          <div className="mt-2 flex flex-wrap gap-x-6 gap-y-1 text-sm text-white/70">
            <span>Status: {isRunning ? 'Running' : 'Stopped'}</span>
            <span>My stake: {getCellFromBaseUnits(currentStake)} CELL</span>
            <span>Required: {getCellFromBaseUnits(minimumStake)} CELL</span>
          </div>
        </div>
        <Button
          label={
            isRunning
              ? 'Stop Mother Hive'
              : missingStake > 0
              ? `Stake ${getCellFromBaseUnits(missingStake)} CELL and Start`
              : 'Start Mother Hive'
          }
          icon={<FontAwesomeIcon icon={isRunning ? faStop : faPlay} />}
          onClick={() => taskMutation.mutate()}
          disabled={!task || (!isRunning && !status?.configured) || busy}
          loading={taskMutation.isLoading || stakeMutation.isLoading}
          className={`w-[230px] ${
            isRunning
              ? 'border border-finnieOrange bg-transparent text-finnieOrange'
              : 'bg-finnieTeal text-finnieBlue-dark'
          }`}
        />
      </section>

      <section className="py-5">
        <div className="mb-3 flex items-end justify-between gap-3">
          <div>
            <h2 className="font-semibold">Resource pool</h2>
            <p className="mt-1 text-xs text-white/55">
              Online Agents reported during the last two minutes
            </p>
          </div>
          {statusQuery.isFetching && <LoadingSpinner className="h-6 w-6" />}
        </div>
        {aggressiveRelayPolicy && status?.relayPolicy && (
          <div
            role="status"
            className="mb-4 flex items-start gap-3 border-l-2 border-finnieOrange bg-finnieOrange/10 px-4 py-3 text-sm text-finnieOrange"
          >
            <FontAwesomeIcon icon={faTriangleExclamation} className="mt-0.5" />
            <span>
              Relay limits are aggressive: CPU {status.relayPolicy.cpuPercent}
              %, GPU {status.relayPolicy.gpuPercent}%, RAM{' '}
              {status.relayPolicy.ramPercent}%. Reduce them in QSDM Edge Control
              on an interactive computer to prevent resource and network stalls.
            </span>
          </div>
        )}
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <Metric
            icon={<FontAwesomeIcon icon={faServer} />}
            label="Agents online"
            value={String(status?.onlineWorkers || 0)}
          />
          <Metric
            icon={<FontAwesomeIcon icon={faMicrochip} />}
            label="Virtual CPU"
            value={`${status?.pooledCpuThreads || 0} threads`}
          />
          <Metric
            icon={<FontAwesomeIcon icon={faMicrochip} />}
            label="Virtual GPU"
            value={`${status?.pooledGpuCount || 0} GPU / ${formatMemory(
              status?.pooledGpuMemoryMiB || 0
            )}`}
          />
          <Metric
            icon={<FontAwesomeIcon icon={faMemory} />}
            label="Virtual RAM"
            value={formatMemory(status?.pooledRamMiB || 0)}
          />
        </div>
      </section>

      <section className="border-t border-white/15 py-5">
        <div className="grid gap-5 xl:grid-cols-[minmax(0,2fr)_minmax(280px,1fr)]">
          <div className="min-w-0">
            <h2 className="font-semibold">Connected Agents</h2>
            <div className="mt-3 overflow-x-auto rounded-md border border-white/10">
              <table className="w-full table-fixed text-left text-xs">
                <thead className="bg-finnieBlue-light-secondary text-finnieTeal-100">
                  <tr>
                    <th className="w-[30%] px-3 py-2 font-medium">Agent</th>
                    <th className="px-3 py-2 font-medium">CPU</th>
                    <th className="px-3 py-2 font-medium">GPU</th>
                    <th className="px-3 py-2 font-medium">RAM</th>
                    <th className="px-3 py-2 font-medium">Jobs</th>
                  </tr>
                </thead>
                <tbody>
                  {onlineWorkers.map((worker) => (
                    <tr
                      key={worker.workerId}
                      className="border-t border-white/10"
                    >
                      <td
                        className="truncate px-3 py-3"
                        title={worker.workerId}
                      >
                        {worker.hostname}
                      </td>
                      <td className="px-3 py-3">{worker.cpuThreads}</td>
                      <td className="px-3 py-3">{worker.gpuCount}</td>
                      <td className="px-3 py-3">
                        {formatMemory(worker.ramMiB)}
                      </td>
                      <td className="px-3 py-3">{worker.completedJobs}</td>
                    </tr>
                  ))}
                  {!onlineWorkers.length && (
                    <tr>
                      <td
                        colSpan={5}
                        className="px-3 py-8 text-center text-white/55"
                      >
                        No online Agents reported by this Relay.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <div className="space-y-4">
            <div className="rounded-md border border-white/10 p-4">
              <h3 className="font-semibold">Relay activity</h3>
              <div className="mt-3 space-y-2 text-xs text-white/70">
                <div className="flex justify-between gap-3">
                  <span>Active jobs</span>
                  <span>{status?.activeJobs || 0}</span>
                </div>
                <div className="flex justify-between gap-3">
                  <span>Verified CPU receipts</span>
                  <span>{status?.verifiedReceipts.cpu || 0}</span>
                </div>
                <div className="flex justify-between gap-3">
                  <span>Verified GPU receipts</span>
                  <span>{status?.verifiedReceipts.gpu || 0}</span>
                </div>
                <div className="flex justify-between gap-3">
                  <span>Verified RAM receipts</span>
                  <span>{status?.verifiedReceipts.ram || 0}</span>
                </div>
              </div>
            </div>
            <div className="rounded-md border border-white/10 p-4">
              <h3 className="font-semibold">Relay limits</h3>
              <div className="mt-3 text-xs text-white/70">
                CPU {status?.relayPolicy?.cpuPercent || 0}% / GPU{' '}
                {status?.relayPolicy?.gpuPercent || 0}% / RAM{' '}
                {status?.relayPolicy?.ramPercent || 0}%
              </div>
            </div>
            <div className="rounded-md border border-white/10 p-4">
              <h3 className="font-semibold">Revenue target</h3>
              <div className="mt-3 text-xs text-white/70">
                Contributors{' '}
                {status?.revenuePolicy.contributorPercent ||
                  QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT}
                % / Mother Hive{' '}
                {status?.revenuePolicy.motherHivePercent ||
                  QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT}
                % / Ecosystem{' '}
                {status?.revenuePolicy.ecosystemPercent ||
                  QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT}
                %
              </div>
              <div className="mt-3 flex justify-between gap-3 text-xs text-white/70">
                <span>Contributor wallet</span>
                <span className="break-all text-right">
                  {status?.revenuePolicy.contributorWalletAddress ||
                    'Waiting for Relay binding'}
                </span>
              </div>
              <div className="mt-3 flex justify-between gap-3 text-xs text-white/70">
                <span>Mother Hive wallet</span>
                <span className="break-all text-right">
                  {status?.revenuePolicy.motherHiveWalletAddress ||
                    'Waiting for Relay binding'}
                </span>
              </div>
              <div className="mt-3 flex justify-between gap-3 text-xs text-white/70">
                <span>Ecosystem treasury</span>
                <span className="break-all text-right">
                  {status?.revenuePolicy.ecosystemWalletAddress ||
                    'Not configured on QSDM Core'}
                </span>
              </div>
              <div className="mt-3 flex justify-between gap-3 text-xs text-white/70">
                <span>Settlement Relay ID</span>
                <span className="break-all text-right">
                  {status?.revenuePolicy.relaySettlementId ||
                    'Waiting for Relay identity'}
                </span>
              </div>
              {!status?.revenuePolicy.settlementActive && (
                <p className="mt-2 text-xs text-finnieOrange">
                  {status?.revenuePolicy.settlementReason ||
                    'Settlement remains disabled until Relay receipts are enforceable on QSDM Core.'}
                </p>
              )}
              {status?.revenuePolicy.settlementActive && (
                <p className="mt-2 text-xs text-green-300">
                  {status.revenuePolicy.settlementReason}
                </p>
              )}
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
