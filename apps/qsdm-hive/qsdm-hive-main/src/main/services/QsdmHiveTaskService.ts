/* eslint-disable camelcase */
/* eslint-disable no-useless-catch */
/* eslint-disable class-methods-use-this */
import { ChildProcess } from 'child_process';
import fs from 'fs';

import { AccountInfo, MemcmpFilter, PublicKey } from 'vendor/qsdm-chain/web3';
import {
  getTaskState,
  getTaskStateKPL,
  getTaskSubmissionInfo,
  initialPropagation,
  IRunningTasks,
  ITaskNodeBase,
  KPL_CONTRACT_ID,
  runPeriodic,
  runTimers,
  updateRewardsQueue,
} from 'vendor/qsdm-chain/taskNode';
import { SERVER_URL } from 'config/server';
import getAverageSlotTime from 'main/controllers/getAverageSlotTime';
import getCurrentSlot from 'main/controllers/getCurrentSlot';
import { getKPLStakingAccountPubKey } from 'main/controllers/getKPLStakingAccountPubKey';
import { getNetworkUrl } from 'main/controllers/getNetworkUrl';
import getStakingAccountPubKey from 'main/controllers/getStakingAccountPubKey';
import { getTaskMetadata } from 'main/controllers/getTaskMetadata';
import { getMyTaskStake } from 'main/controllers/tasks';
import db from 'main/db';
import { getMainSystemAccountKeypair } from 'main/node/helpers';
import { electronStoreService } from 'main/node/helpers/electronStoreService';
import { getK2NetworkUrl } from 'main/node/helpers/k2NetworkUrl';
import { sleep } from 'main/util';
import { ErrorType } from 'models';
import {
  MAINNET_RPC_URL,
  TESTNET_RPC_URL,
} from 'renderer/features/shared/constants';
import {
  getProgramAccountFilter,
  normalizeQsdmTaskType,
  throwDetailedError,
} from 'utils';

import { ATTENTION_TASK_ID, TASK_CONTRACT_ID } from '../../config/node';
import {
  buildQsdmTaskReadUrls,
  QSDM_TASK_RUNTIME_MODE,
} from '../../config/qsdm';
import {
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  isQsdmHiveInternalTaskId,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
} from '../../config/qsdmSystemTasks';
import { SystemDbKeys } from '../../config/systemDbKeys';
import { getAppDataPath } from '../node/helpers/getAppDataPath';
import { Namespace, namespaceInstance } from '../node/helpers/Namespace';

import { K2TasksDataFetchOptions } from './QsdmHiveTaskService/types';
import { submitQsdmTaskActionIntent } from './qsdmTaskActions';
import { getQsdmTaskActionSender } from './qsdmTaskActionSigner';
import { qsdmGetFirstJson } from './qsdmHttpRead';
import { assertQsdmMinerEnrollmentReady } from './qsdmMinerEnrollment';
import {
  getQsdmTaskStakeOwnership,
  normalizeQsdmNativeCellAmountMapToDenomination,
  normalizeQsdmNativeCellAmountToDenomination,
  QsdmTaskStakeOwnership,
} from './qsdmTaskStake';
import {
  adoptQsdmMinerSystemProcess,
  assertQsdmMotherHiveConfigured,
  getQsdmMinerSystemProcessInfo,
  getQsdmSystemTaskById,
  isQsdmEdgeWorkerSystemTask,
  isQsdmMinerSystemTask,
  isQsdmMotherHiveSystemTask,
  isQsdmSkyFangLinkSystemTask,
  isQsdmSystemTask,
  mergeQsdmSystemTasks,
  requireQsdmSkyFangWalletLinkedForSkyFangLink,
  startQsdmEdgeWorkerSystemProcess,
  startQsdmMotherHiveSystemProcess,
  startQsdmSkyFangLinkSystemProcess,
  stopExtraQsdmMinerSystemProcesses,
} from './qsdmSystemTasks';
import sdk from './sdk';
import {
  fetchWithRetry,
  getCompleteTaskFromCache,
  getTaskDataFromCache,
  getTasksFromCache,
  saveBaseStatesToCache,
  updateTaskCacheRecord,
} from './tasks-cache-utils';

import type { QsdmTaskResponse, QsdmTasksListResponse } from 'models/api/qsdm';
import type {
  RawTaskData,
  Submission,
  SubmissionsPerRound,
  TaskMetadata,
} from 'models';

const FIFTEEN_MINUTES_IN_MS = 15 * 60 * 1000;
const QSDM_TASK_CATALOG_REFRESH_INTERVAL_MS = 15 * 1000;

type EligibleTasksResponse = {
  eligibleTasks: string[];
};

const filterUserFacingQsdmTasks = (tasks: RawTaskData[]) =>
  tasks.filter((task) => !isQsdmHiveInternalTaskId(task.task_id));

const normalizeQsdmNativeTaskForHive = (task: RawTaskData): RawTaskData => ({
  ...task,
  task_type: normalizeQsdmTaskType({
    taskType: task.task_type,
    tokenType: task.token_type,
  }),
  total_bounty_amount:
    normalizeQsdmNativeCellAmountToDenomination(task.total_bounty_amount) || 0,
  bounty_amount_per_round:
    normalizeQsdmNativeCellAmountToDenomination(task.bounty_amount_per_round) ||
    0,
  available_balances: normalizeQsdmNativeCellAmountMapToDenomination(
    task.available_balances
  ),
  stake_list: normalizeQsdmNativeCellAmountMapToDenomination(task.stake_list),
  total_stake_amount:
    normalizeQsdmNativeCellAmountToDenomination(task.total_stake_amount) || 0,
  reward_pool_amount: normalizeQsdmNativeCellAmountToDenomination(
    task.reward_pool_amount
  ),
  pending_reward_amount: normalizeQsdmNativeCellAmountToDenomination(
    task.pending_reward_amount
  ),
  total_reward_paid_amount: normalizeQsdmNativeCellAmountToDenomination(
    task.total_reward_paid_amount
  ),
});

const fetchQsdmNativeJson = async <T>(
  path: string,
  timeout: number
): Promise<T> => qsdmGetFirstJson<T>(buildQsdmTaskReadUrls(path), { timeout });

function getLatestSubmission(
  publicKey: string,
  submissions: SubmissionsPerRound
): {
  latestSubmission: Submission | undefined;
  latestRound: number | undefined;
} {
  let latestSubmission: Submission | undefined;
  let latestRound: number | undefined;

  // eslint-disable-next-line guard-for-in
  for (const round in submissions) {
    const roundNumber = parseInt(round, 10);
    const submission = submissions[roundNumber][publicKey];

    if (
      submission &&
      (latestRound === undefined || roundNumber > latestRound)
    ) {
      latestSubmission = submission;
      latestRound = roundNumber;
    }
  }
  return { latestSubmission, latestRound };
}

export class QsdmHiveTaskService {
  public RUNNING_TASKS: IRunningTasks<ITaskNodeBase> = {};

  public allTaskPubkeys: string[] = [];

  public kplTaskPubKeys: string[] = [];

  public privateTaskPubKeys: string[] = [];

  public timerForRewards = 0;

  private startedTasksData:
    | Omit<RawTaskData, 'is_running'>[]
    | null
    | undefined = [];

  private taskMetadata: any = {};

  private qsdmNativeTasksById: Record<string, RawTaskData> = {};

  private submissionCheckIntervals: Record<string, NodeJS.Timeout> = {};

  private submissionFetchingTimeouts: Record<string, NodeJS.Timeout> = {};

  private submissionFetchingIntervals: Record<string, NodeJS.Timer> = {};

  private qsdmSystemTaskRestartTimers: Record<string, NodeJS.Timeout> = {};

  private isInitialized = false;

  public nodePropagationInterval: NodeJS.Timeout | null = null;

  private clearQsdmSystemTaskRestart(taskAccountPubKey: string) {
    const timer = this.qsdmSystemTaskRestartTimers[taskAccountPubKey];
    if (timer) {
      clearTimeout(timer);
      delete this.qsdmSystemTaskRestartTimers[taskAccountPubKey];
    }
  }

  private scheduleQsdmSystemTaskRestart(
    taskAccountPubKey: string,
    reason: string
  ) {
    if (this.qsdmSystemTaskRestartTimers[taskAccountPubKey]) {
      return;
    }

    this.qsdmSystemTaskRestartTimers[taskAccountPubKey] = setTimeout(
      async () => {
        delete this.qsdmSystemTaskRestartTimers[taskAccountPubKey];

        try {
          const intendedRunningTasks = await this.getRunningTaskPubKeys();
          if (!intendedRunningTasks.includes(taskAccountPubKey)) {
            return;
          }

          if (this.RUNNING_TASKS[taskAccountPubKey]) {
            return;
          }

          const restored = await this.reconcileQsdmSystemTaskRuntime(
            taskAccountPubKey,
            { submitStartAction: false }
          );
          if (!restored) {
            console.warn(
              `QSDM system task ${taskAccountPubKey} did not restart after ${reason}; will retry while it remains marked as running.`
            );
            this.scheduleQsdmSystemTaskRestart(taskAccountPubKey, reason);
          }
        } catch (error) {
          console.warn(
            `QSDM system task ${taskAccountPubKey} restart failed after ${reason}; will retry while it remains marked as running.`,
            error
          );
          this.scheduleQsdmSystemTaskRestart(taskAccountPubKey, reason);
        }
      },
      5000
    );
  }

  private async stopCompletedQsdmSkyFangLinkTask(
    taskAccountPubKey: string,
    taskInfo: RawTaskData,
    sender: string
  ): Promise<boolean> {
    void taskAccountPubKey;
    void taskInfo;
    void sender;
    // Sky Fang Link is an ongoing stake-weighted integration task. Claimed
    // rewards from earlier rounds must not stop or suppress the verifier.
    return false;
  }

  updateStartedTasksData(
    taskId: string,
    updater: (
      task: Omit<RawTaskData, 'is_running'>
    ) => Omit<RawTaskData, 'is_running'>
  ) {
    this.startedTasksData = this.startedTasksData?.map((task) => {
      if (task.task_id === taskId) {
        return updater(task);
      }
      return task;
    });
  }

  private upsertStartedTaskData(taskId: string, taskInfo: RawTaskData) {
    if (!this.startedTasksData) {
      this.startedTasksData = [taskInfo];
      return;
    }

    const taskIndex = this.startedTasksData.findIndex(
      (task) => task.task_id === taskId
    );

    if (taskIndex !== -1) {
      this.startedTasksData[taskIndex] = {
        ...taskInfo,
        available_balances:
          this.startedTasksData[taskIndex]?.available_balances ||
          taskInfo.available_balances,
        submissions:
          this.startedTasksData[taskIndex]?.submissions || taskInfo.submissions,
      };
      return;
    }

    this.startedTasksData.push(taskInfo);
  }

  /**
   * @dev: this functions is preparing QSDM Hive to work in a few crucial steps:
   * 1. Fetch all tasks from the Task program
   * 2. Get the state of the tasks from the database
   * 3. Watch for changes in the tasks
   */
  async initializeTasks() {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      try {
        await this.fetchAllTaskIds();
      } catch (error) {
        console.warn(
          'Native QSDM task discovery failed during initialization; using cached tasks.',
          error
        );
      }

      try {
        await this.fetchStartedTaskData(true);
        await this.reconcileQsdmSystemTaskRuntime(QSDM_MINER_SYSTEM_TASK_ID, {
          submitStartAction: true,
        });
        const runningTasks = await this.getRunningTaskPubKeys();
        const edgeWorkerTaskIds = [
          QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
          QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
          QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
        ];
        await Promise.all(
          edgeWorkerTaskIds
            .filter((taskId) => runningTasks.includes(taskId))
            .map((taskId) =>
              this.reconcileQsdmSystemTaskRuntime(taskId, {
                submitStartAction: true,
              })
            )
        );
        if (runningTasks.includes(QSDM_MOTHER_HIVE_SYSTEM_TASK_ID)) {
          await this.reconcileQsdmSystemTaskRuntime(
            QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
            { submitStartAction: true }
          );
        }
      } catch (error) {
        console.warn(
          'Native QSDM started-task refresh failed during initialization; continuing offline.',
          error
        );
        this.startedTasksData = this.startedTasksData || [];
      }

      this.watchTasks();
      return;
    }

    /**
     * @dev fetches all availbe tasks from K2
     */
    this.fetchAllTaskIds();
    /**
     * @dev get all started tassks and their newest state,
     * now we can get upgradeable tasks ids and filter them out from
     * availableTasksIds
     */
    const { upgradeableTaskIds } = await this.fetchStartedTaskData(true);
    this.allTaskPubkeys = this.allTaskPubkeys.filter(
      (taskPubKey) => !upgradeableTaskIds.includes(taskPubKey)
    );

    this.watchTasks();
  }

  async runTimers() {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      return;
    }

    const kplTaskIds: string[] = [];
    const startedTasks = this.startedTasksData?.reduce((acc, task) => {
      if (this.RUNNING_TASKS[task.task_id]) {
        const isKplTask = !!task?.token_type;
        acc.push({
          ...task,
          task_type: isKplTask ? 'KPL' : 'CELL',
        });
        if (isKplTask) {
          kplTaskIds.push(task.task_id);
        }
      }
      return acc;
    }, [] as RawTaskData[]);

    if (!startedTasks) {
      return;
    }

    runTimers({
      KPL_tasks: kplTaskIds,
      selectedTasks: startedTasks,
      runningTasks: this.RUNNING_TASKS,
      setTimerForRewards: this.setTimerForRewards,
      networkURL: getNetworkUrl(),
    });
  }

  public stopSubmissionCheck(taskAccountPubKey: string): void {
    if (this.submissionCheckIntervals[taskAccountPubKey]) {
      clearInterval(this.submissionCheckIntervals[taskAccountPubKey]);
      delete this.submissionCheckIntervals[taskAccountPubKey]; // Remove the interval ID from the tracking object
    }
  }

  public stopSubmissionFetching(taskAccountPubKey: string): void {
    if (this.submissionFetchingTimeouts[taskAccountPubKey]) {
      clearTimeout(this.submissionFetchingTimeouts[taskAccountPubKey]);
      delete this.submissionFetchingTimeouts[taskAccountPubKey];
    }
    if (this.submissionFetchingIntervals[taskAccountPubKey]) {
      clearInterval(this.submissionFetchingIntervals[taskAccountPubKey]);
      delete this.submissionFetchingIntervals[taskAccountPubKey];
    }
  }

  public getTaskByTaskAuditProgramId(taskAuditProgramId: string) {
    return this.startedTasksData?.find((task) => {
      return task.task_audit_program === taskAuditProgramId;
    });
  }

  private cacheQsdmNativeTasks(tasks: RawTaskData[]) {
    this.qsdmNativeTasksById = tasks.reduce((acc, task) => {
      acc[task.task_id] = normalizeQsdmNativeTaskForHive(task);
      return acc;
    }, {} as Record<string, RawTaskData>);
  }

  private async fetchQsdmNativeTasks(): Promise<RawTaskData[]> {
    try {
      const response = await fetchQsdmNativeJson<QsdmTasksListResponse>(
        '/tasks',
        4000
      );
      const tasks = filterUserFacingQsdmTasks(
        mergeQsdmSystemTasks(response.tasks || [])
      ).map(normalizeQsdmNativeTaskForHive);
      this.cacheQsdmNativeTasks(tasks);
      return tasks;
    } catch (error) {
      console.warn(
        'QSDM native task list fetch failed; falling back to cache.',
        error instanceof Error ? error.message : String(error)
      );

      const namespacePath = `${getAppDataPath()}/namespace`;
      const startedTaskIds = fs.existsSync(namespacePath)
        ? fs
            .readdirSync(namespacePath, { withFileTypes: true })
            .filter((item) => item.isDirectory())
            .map((item) => item.name)
        : [];
      const cachedTasks = filterUserFacingQsdmTasks(
        mergeQsdmSystemTasks((await getTasksFromCache(startedTaskIds)) || [])
      ).map(normalizeQsdmNativeTaskForHive);
      this.cacheQsdmNativeTasks(cachedTasks);
      return cachedTasks;
    }
  }

  private async getQsdmNativeTaskState(
    taskAccountPubKey: string
  ): Promise<RawTaskData> {
    const systemTaskTemplate = getQsdmSystemTaskById(taskAccountPubKey, {
      is_running: Boolean(this.RUNNING_TASKS[taskAccountPubKey]),
    });

    try {
      const response = await fetchQsdmNativeJson<QsdmTaskResponse>(
        `/tasks/${encodeURIComponent(taskAccountPubKey)}`,
        4000
      );
      const task =
        getQsdmSystemTaskById(taskAccountPubKey, {
          ...response.task,
          is_running: Boolean(this.RUNNING_TASKS[taskAccountPubKey]),
        }) || response.task;
      const normalizedTask = normalizeQsdmNativeTaskForHive(task);
      this.qsdmNativeTasksById[normalizedTask.task_id] = normalizedTask;
      return normalizedTask;
    } catch (error) {
      const cachedTask = this.qsdmNativeTasksById[taskAccountPubKey];
      if (cachedTask) {
        return cachedTask;
      }
      if (systemTaskTemplate) {
        const normalizedTemplate =
          normalizeQsdmNativeTaskForHive(systemTaskTemplate);
        this.qsdmNativeTasksById[normalizedTemplate.task_id] =
          normalizedTemplate;
        return normalizedTemplate;
      }
      throw error;
    }
  }

  public async getTaskStateKPL(
    taskAccountPubKey: string,
    options?: K2TasksDataFetchOptions
  ): Promise<RawTaskData> {
    console.log('attempting to fetch KPL task state for ', taskAccountPubKey);
    try {
      const result = await getTaskStateKPL(
        sdk.k2Connection,
        taskAccountPubKey,
        {
          is_available_balances_required: options?.withAvailableBalances,
          is_distribution_required: options?.withDistributions,
          is_stake_list_required: options?.withStakeList,
          is_submission_required: options?.withSubmissions,
        }
      );

      const taskData = {
        ...result,
        task_id: taskAccountPubKey,
        task_type: 'KPL',
      } as any;

      return taskData;
    } catch (error) {
      console.error('error fetching KPL task state', error);
      throw error;
    }
  }

  public async getTaskStateCell(
    taskAccountPubKey: string,
    options?: K2TasksDataFetchOptions
  ): Promise<any> {
    console.log(
      'attempting to fetch native task state for ',
      taskAccountPubKey
    );
    try {
      const result = await getTaskState(sdk.k2Connection, taskAccountPubKey, {
        is_available_balances_required: options?.withAvailableBalances,
        is_distribution_required: options?.withDistributions,
        is_stake_list_required: options?.withStakeList,
        is_submission_required: options?.withSubmissions,
      });

      if (!result || !(result as RawTaskData).task_name) {
        throw new Error('task data not found');
      }

      const taskData = {
        ...result,
        task_id: taskAccountPubKey,
        task_type: 'CELL',
      } as RawTaskData;

      return taskData;
    } catch (error) {
      console.error('error fetching native task state', error);
      throw error;
    }
  }

  public async getTaskState(
    taskAccountPubKey: string,
    options?: K2TasksDataFetchOptions
  ): Promise<RawTaskData> {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      return this.getQsdmNativeTaskState(taskAccountPubKey);
    }

    try {
      const taskIsCellType = this.allTaskPubkeys.includes(taskAccountPubKey);
      const taskIsKPLType = this.kplTaskPubKeys.includes(taskAccountPubKey);
      if (taskIsCellType) {
        return this.getTaskStateCell(taskAccountPubKey, options);
      } else if (taskIsKPLType) {
        return this.getTaskStateKPL(taskAccountPubKey, options);
      } else {
        try {
          return await this.getTaskStateKPL(taskAccountPubKey, options);
        } catch (error) {
          try {
            return await this.getTaskStateCell(taskAccountPubKey, options);
          } catch (error) {
            throw error;
          }
        }
      }
    } catch (error) {
      throw error;
    }
  }

  private async checkTaskSubmission(task: RawTaskData) {
    const stakingPublicKey = await getStakingAccountPubKey();

    const currentSlot = await getCurrentSlot();
    const currentRound = Math.floor(
      (currentSlot - task.starting_slot) / task.round_time
    );
    const submissionsInCache =
      (await getTaskDataFromCache(task.task_id, 'submissions'))?.submissions ||
      {};
    const latestSubmissionData = getLatestSubmission(
      stakingPublicKey,
      submissionsInCache
    );
    const submissionsAreTooOutdated =
      !latestSubmissionData?.latestRound ||
      (latestSubmissionData?.latestRound &&
        currentRound - latestSubmissionData.latestRound > 3);

    const runningTask = this.RUNNING_TASKS[task.task_id];

    const stopSubmissionCheckAndRetry = () => {
      this.stopSubmissionCheck(task.task_id);
      this.stopSubmissionFetching(task.task_id);
      runningTask.child.kill('SIGTERM');
      // TO DO: find why sometimes kill() doesn't trigger the exit event and we have to emit it manually
      runningTask.child.emit('exit', 0, null);
    };

    if (!!runningTask && submissionsAreTooOutdated) {
      stopSubmissionCheckAndRetry();
    }
  }

  private watchTasks() {
    if (this.isInitialized) return;
    this.isInitialized = true;
    let refreshInProgress = false;
    const refreshInterval =
      QSDM_TASK_RUNTIME_MODE === 'qsdm-native'
        ? QSDM_TASK_CATALOG_REFRESH_INTERVAL_MS
        : FIFTEEN_MINUTES_IN_MS;
    setInterval(async () => {
      if (refreshInProgress) return;
      refreshInProgress = true;
      try {
        await this.fetchAllTaskIds();
        await this.fetchStartedTaskData();
      } catch (e) {
        console.error(e);
      } finally {
        refreshInProgress = false;
      }
    }, refreshInterval);
  }

  async getStartedTasks(force?: boolean): Promise<RawTaskData[]> {
    if (force) {
      await this.fetchStartedTaskData();
    }

    while (!this.isInitialized) {
      await sleep(1000);
    }

    if (!this.startedTasksData) {
      const startedTasksPubKeys: Array<string> =
        await this.getStartedTasksPubKeys();

      // Try to load from cache if the data has not been fetched
      const cachedData = await getTasksFromCache(startedTasksPubKeys);

      if (cachedData) {
        this.startedTasksData = cachedData;
      } else {
        // try to refetch the data with 3 retries, if there is no cache available
        await fetchWithRetry(this.fetchStartedTaskData);
      }
    }

    return (this.startedTasksData ?? []).map((task) => ({
      ...task,
      is_running: Boolean(this.RUNNING_TASKS[task.task_id]),
    }));
  }

  async startTask(
    taskAccountPubKey: string,
    namespace: ITaskNodeBase,
    childTaskProcess: ChildProcess,
    expressAppPort: number,
    secret: string,
    taskInfo?: RawTaskData
  ): Promise<void> {
    this.clearQsdmSystemTaskRestart(taskAccountPubKey);

    this.RUNNING_TASKS[taskAccountPubKey] = {
      namespace,
      child: childTaskProcess,
      expressAppPort,
      secret,
    };

    await this.addRunningTaskPubKey(taskAccountPubKey);

    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      if (taskInfo) {
        this.upsertStartedTaskData(taskAccountPubKey, taskInfo);
      }
      return;
    }

    try {
      if (this.nodePropagationInterval) {
        clearInterval(this.nodePropagationInterval);
      }
      this.stopSubmissionCheck(taskAccountPubKey);
      this.stopSubmissionFetching(taskAccountPubKey);
      const runningTasks = (await this.getStartedTasks()).filter((task) => {
        return !!this.RUNNING_TASKS[task.task_id];
      });
      const curr_subDomain = await namespaceInstance.storeGet('subdomain');
      const mainSystemAccount = await getMainSystemAccountKeypair();
      initialPropagation(
        runningTasks,
        ATTENTION_TASK_ID,
        namespaceInstance,
        mainSystemAccount,
        `http://${curr_subDomain}`,
        true
      ).then(() => {
        this.nodePropagationInterval = setInterval(
          () =>
            runPeriodic(
              runningTasks,
              namespaceInstance,
              mainSystemAccount,
              `http://${curr_subDomain}`,
              true
            ),
          300000
        );
      });
    } catch (error: any) {
      console.error(error.message);
    }

    const submissionsInCache =
      (await getTaskDataFromCache(taskAccountPubKey, 'submissions'))
        ?.submissions || {};
    if (taskInfo) {
      this.upsertStartedTaskData(taskAccountPubKey, taskInfo);
    }

    const taskRawData = this.startedTasksData?.find(
      (task) => task.task_id === taskAccountPubKey
    );

    await this.runTimers();

    if (!taskRawData) {
      return;
    }
    const averageSlotTime = await getAverageSlotTime();
    const roundTimeInMs = taskRawData.round_time * averageSlotTime;

    const currentSlot = await getCurrentSlot();
    const slotsSinceTaskCreation = currentSlot - taskRawData.starting_slot;
    const roundsSinceTaskCreation =
      slotsSinceTaskCreation / taskRawData.round_time;
    const roundFractionMissingToCompleteCurrentRound =
      roundsSinceTaskCreation % 1;
    const slotsToCompleteCurrentRound =
      roundFractionMissingToCompleteCurrentRound * taskRawData.round_time;
    const slotsToWaitBeforeFetchingSubmission =
      slotsToCompleteCurrentRound + taskRawData.submission_window;
    const timeToWaitBeforeFetchingSubmissionInMs =
      slotsToWaitBeforeFetchingSubmission * averageSlotTime;

    const fetchSubmissions = async () => {
      console.log('fetching submissions for task', taskAccountPubKey);
      const taskSubmissions = await getTaskSubmissionInfo(
        sdk.k2Connection,
        taskAccountPubKey,
        taskRawData.task_type
      );
      console.log('fetched submissions for task', taskAccountPubKey);
      await updateTaskCacheRecord(
        taskAccountPubKey,
        taskSubmissions,
        'submissions'
      );
    };

    const currentRound = Math.floor(
      (currentSlot - taskRawData.starting_slot) / taskRawData.round_time
    );
    const correspondingStakingPublicKey = taskRawData.token_type
      ? await getKPLStakingAccountPubKey()
      : await getStakingAccountPubKey();

    const latestSubmissionData = getLatestSubmission(
      correspondingStakingPublicKey,
      submissionsInCache
    );
    const submissionsAreTooOutdated =
      latestSubmissionData?.latestRound &&
      currentRound - latestSubmissionData.latestRound >= 3;
    if (submissionsAreTooOutdated) {
      console.log(
        'fetching submissions when starting task because cache is outdated'
      );
      await fetchSubmissions();
    }
    this.submissionFetchingTimeouts[taskAccountPubKey] = setTimeout(
      async () => {
        await fetchSubmissions();
        const submissionsFetchingInterval = setInterval(async () => {
          const taskIsRunning = !!this.RUNNING_TASKS[taskAccountPubKey];
          const makeSureToClearInterval = () => {
            this.stopSubmissionFetching(taskAccountPubKey);
            clearInterval(submissionsFetchingInterval);
          };
          if (taskIsRunning) {
            await fetchSubmissions();
          } else {
            makeSureToClearInterval();
          }
        }, roundTimeInMs);
        this.submissionFetchingIntervals[taskAccountPubKey] =
          submissionsFetchingInterval;
      },
      timeToWaitBeforeFetchingSubmissionInMs
    );

    const taskIndex = this.startedTasksData?.findIndex(
      (task) => task.task_id === taskAccountPubKey
    );
    const baseTimeIntervalForSubmissionsCheck = 3.5 * roundTimeInMs;
    const safeIntervalDelay = ((taskIndex || 0) + 1) * 30 * 1000;
    const finalTimeInterval =
      baseTimeIntervalForSubmissionsCheck + safeIntervalDelay;

    this.submissionCheckIntervals[taskAccountPubKey] = setInterval(() => {
      const taskRawData = this.startedTasksData?.find(
        (task) => task.task_id === taskAccountPubKey
      );

      if (taskRawData) {
        this.checkTaskSubmission(taskRawData);
      }
    }, finalTimeInterval);
  }

  public async reconcileQsdmSystemTaskRuntime(
    taskAccountPubKey: string,
    options: { submitStartAction?: boolean } = {}
  ): Promise<boolean> {
    const isMinerSystemTask = isQsdmMinerSystemTask(taskAccountPubKey);
    const isEdgeWorkerSystemTask =
      isQsdmEdgeWorkerSystemTask(taskAccountPubKey);
    const isMotherHiveSystemTask =
      isQsdmMotherHiveSystemTask(taskAccountPubKey);
    const isSkyFangLinkSystemTask =
      isQsdmSkyFangLinkSystemTask(taskAccountPubKey);

    if (
      QSDM_TASK_RUNTIME_MODE !== 'qsdm-native' ||
      (!isMinerSystemTask &&
        !isEdgeWorkerSystemTask &&
        !isMotherHiveSystemTask &&
        !isSkyFangLinkSystemTask)
    ) {
      return Boolean(this.RUNNING_TASKS[taskAccountPubKey]);
    }

    const sender = getQsdmTaskActionSender();

    if (this.RUNNING_TASKS[taskAccountPubKey]) {
      if (isSkyFangLinkSystemTask && sender) {
        const taskInfo = await this.getQsdmNativeTaskState(taskAccountPubKey);
        if (
          await this.stopCompletedQsdmSkyFangLinkTask(
            taskAccountPubKey,
            taskInfo,
            sender
          )
        ) {
          return false;
        }
      }

      if (isSkyFangLinkSystemTask) {
        try {
          await requireQsdmSkyFangWalletLinkedForSkyFangLink();
        } catch (error) {
          console.warn(
            'QSDM Sky Fang Link verifier is running, but live wallet-link verification is not ready yet; keeping the verifier active.',
            error
          );
          return true;
        }
      }
      return true;
    }

    if (!sender) {
      console.warn(
        `QSDM system task ${taskAccountPubKey} could not be restored because no QSDM task signer is configured.`
      );
      return false;
    }

    let ownership: QsdmTaskStakeOwnership = {
      sender,
      currentStakeCell: 0,
      currentStakeDenomination: 0,
      foreignStakeCell: 0,
      foreignParticipants: [],
      runningForCurrentSender: false,
      runningForOtherSender: false,
    };
    if (!isMinerSystemTask) {
      try {
        ownership = await getQsdmTaskStakeOwnership(taskAccountPubKey, sender);
      } catch (error) {
        console.warn(
          'QSDM system task stake ownership could not be verified.',
          error
        );
        return false;
      }
    }

    if (!isMinerSystemTask && ownership.currentStakeCell <= 0) {
      console.warn(
        `QSDM system task ${taskAccountPubKey} could not be restored because signer ${sender} has no confirmed stake.`
      );
      return false;
    }

    const taskInfo = await this.getQsdmNativeTaskState(taskAccountPubKey);

    if (isSkyFangLinkSystemTask) {
      if (
        await this.stopCompletedQsdmSkyFangLinkTask(
          taskAccountPubKey,
          taskInfo,
          sender
        )
      ) {
        return false;
      }

      try {
        await requireQsdmSkyFangWalletLinkedForSkyFangLink();
      } catch (error) {
        console.warn(
          'QSDM Sky Fang Link verifier is being restored while live wallet-link verification is not ready yet; the worker will keep checking and only submit after the wallet is linked.',
          error
        );
      }
    }

    const mainSystemAccount = await getMainSystemAccountKeypair();
    const namespace = new Namespace({
      taskTxId: taskAccountPubKey,
      serverApp: null as any,
      mainSystemAccount,
      db,
      taskType: 'CELL',
      rpcUrl: getK2NetworkUrl(),
      taskData: taskInfo,
    });

    if (isEdgeWorkerSystemTask) {
      try {
        if (options.submitStartAction && !ownership.runningForCurrentSender) {
          await submitQsdmTaskActionIntent({
            taskId: taskAccountPubKey,
            action: 'start',
            payload: {
              mode: taskAccountPubKey,
              no_expiry: true,
              local_process: true,
              restored_process: true,
            },
          });
        }
      } catch (error) {
        console.warn(
          'QSDM Edge Worker could not submit the signed start action during restore; leaving it stopped.',
          error
        );
        return false;
      }

      const { child, secret } = startQsdmEdgeWorkerSystemProcess(
        taskAccountPubKey,
        taskInfo
      );

      child.once('exit', async (code, signal) => {
        console.error(
          `${taskInfo.task_name} restored task process ${child.pid} exited with code ${code} and signal ${signal}`
        );
        await this.handleQsdmNativeTaskProcessExit(
          taskAccountPubKey,
          'qsdm-system-process-exit'
        );
      });

      await this.startTask(taskAccountPubKey, namespace, child, 0, secret, {
        ...taskInfo,
        is_running: true,
        stake_list: {
          ...(taskInfo.stake_list || {}),
          [sender]: ownership.currentStakeDenomination,
        },
      });

      console.log(`Restored QSDM Edge Worker task ${taskAccountPubKey}.`);
      return true;
    }

    if (isMotherHiveSystemTask) {
      try {
        assertQsdmMotherHiveConfigured();
      } catch (error) {
        console.warn('Mother Hive Task is not paired with a Relay.', error);
        return false;
      }

      try {
        if (options.submitStartAction && !ownership.runningForCurrentSender) {
          await submitQsdmTaskActionIntent({
            taskId: taskAccountPubKey,
            action: 'start',
            payload: {
              mode: 'qsdm-hive-mother',
              no_expiry: true,
              local_process: true,
              restored_process: true,
            },
          });
        }
      } catch (error) {
        console.warn(
          'Mother Hive Task could not submit the signed start action during restore; leaving it stopped.',
          error
        );
        return false;
      }

      let runtime: ReturnType<typeof startQsdmMotherHiveSystemProcess>;
      try {
        runtime = startQsdmMotherHiveSystemProcess();
      } catch (error) {
        console.warn(
          'Mother Hive Task could not reconnect to its paired Relay.',
          error
        );
        return false;
      }
      const { child, secret } = runtime;

      child.once('exit', async (code, signal) => {
        console.error(
          `${taskInfo.task_name} restored task process ${child.pid} exited with code ${code} and signal ${signal}`
        );
        await this.handleQsdmNativeTaskProcessExit(
          taskAccountPubKey,
          'qsdm-system-process-exit'
        );
      });

      await this.startTask(taskAccountPubKey, namespace, child, 0, secret, {
        ...taskInfo,
        is_running: true,
        stake_list: {
          ...(taskInfo.stake_list || {}),
          [sender]: ownership.currentStakeDenomination,
        },
      });

      console.log('Restored Mother Hive Task in QSDM Hive.');
      return true;
    }

    if (isSkyFangLinkSystemTask) {
      try {
        if (options.submitStartAction && !ownership.runningForCurrentSender) {
          await submitQsdmTaskActionIntent({
            taskId: taskAccountPubKey,
            action: 'start',
            payload: {
              mode: 'qsdm-skyfang-wallet-link',
              no_expiry: true,
              local_process: true,
              restored_process: true,
            },
          });
        }
      } catch (error) {
        console.warn(
          'QSDM Sky Fang Link could not submit the signed start action during restore; leaving it stopped.',
          error
        );
        return false;
      }

      const { child, secret } = startQsdmSkyFangLinkSystemProcess(taskInfo);

      child.once('exit', async (code, signal) => {
        console.error(
          `${taskInfo.task_name} restored task process ${child.pid} exited with code ${code} and signal ${signal}`
        );
        await this.handleQsdmNativeTaskProcessExit(
          taskAccountPubKey,
          'qsdm-system-process-exit'
        );
      });

      await this.startTask(taskAccountPubKey, namespace, child, 0, secret, {
        ...taskInfo,
        is_running: true,
        stake_list: {
          ...(taskInfo.stake_list || {}),
          [sender]: ownership.currentStakeDenomination,
        },
      });

      console.log(`Restored QSDM Sky Fang Link task ${taskAccountPubKey}.`);
      return true;
    }

    try {
      await assertQsdmMinerEnrollmentReady();
    } catch (error) {
      console.warn(
        'QSDM miner process is not backed by an active enrollment for this signer and GPU.',
        error
      );
      return false;
    }

    const processInfo = await getQsdmMinerSystemProcessInfo();
    if (!processInfo) {
      return false;
    }
    await stopExtraQsdmMinerSystemProcesses(processInfo.pid);

    const { child, secret } = adoptQsdmMinerSystemProcess(processInfo);

    child.once('exit', async (code, signal) => {
      console.error(
        `${taskInfo.task_name} adopted task process ${child.pid} exited with code ${code} and signal ${signal}`
      );
      await this.handleQsdmNativeTaskProcessExit(
        taskAccountPubKey,
        'qsdm-system-process-exit'
      );
    });

    await this.startTask(taskAccountPubKey, namespace, child, 0, secret, {
      ...taskInfo,
      is_running: true,
      stake_list: {
        ...(taskInfo.stake_list || {}),
        [sender]: ownership.currentStakeDenomination,
      },
    });

    console.log(
      `Adopted existing QSDM miner process ${processInfo.pid} into Hive task ${taskAccountPubKey}.`
    );
    return true;
  }

  async setTimerForRewards(value: number) {
    electronStoreService.setTimeToNextRewardAsSlots(value);
  }

  async updateRewardsQueue() {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      return;
    }

    const kplTaskIds =
      this.startedTasksData
        ?.filter((task) => !!task?.token_type)
        .map((task) => task.task_id) || [];

    await updateRewardsQueue(
      kplTaskIds,
      this.setTimerForRewards,
      sdk.k2Connection
    );
  }

  stopTasksOnAppQuit() {
    Object.keys(this.qsdmSystemTaskRestartTimers).forEach((taskPubKey) => {
      this.clearQsdmSystemTaskRestart(taskPubKey);
    });

    const runningTasks = Object.keys(this.RUNNING_TASKS) || [];
    runningTasks.forEach((taskPubKey) => {
      if (
        QSDM_TASK_RUNTIME_MODE === 'qsdm-native' &&
        isQsdmSystemTask(taskPubKey)
      ) {
        this.stopSubmissionCheck(taskPubKey);
        this.stopSubmissionFetching(taskPubKey);
        this.RUNNING_TASKS[taskPubKey].child.kill('SIGTERM');
        delete this.RUNNING_TASKS[taskPubKey];
        return;
      }

      this.RUNNING_TASKS[taskPubKey].child.kill('SIGTERM');
      this.RUNNING_TASKS[taskPubKey].child.emit('exit', null, null);
    });
  }

  async stopTask(
    taskAccountPubKey: string,
    skipRemoveFromRunningTasks?: boolean
  ) {
    if (!this.RUNNING_TASKS[taskAccountPubKey]) {
      if (this.qsdmSystemTaskRestartTimers[taskAccountPubKey]) {
        this.clearQsdmSystemTaskRestart(taskAccountPubKey);
        if (!skipRemoveFromRunningTasks) {
          await this.removeRunningTaskPubKey(taskAccountPubKey);
        }
        if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
          try {
            await submitQsdmTaskActionIntent({
              taskId: taskAccountPubKey,
              action: 'stop',
              payload: { reason: 'local-task-stop' },
            });
          } catch (error: any) {
            return throwDetailedError({
              detailed: error?.message || error,
              type: ErrorType.GENERIC,
            });
          }
        }
        return;
      }

      return throwDetailedError({
        detailed: 'No such task is running',
        type: ErrorType.NO_RUNNING_TASK,
      });
    }

    this.clearQsdmSystemTaskRestart(taskAccountPubKey);

    if (isQsdmSystemTask(taskAccountPubKey)) {
      this.RUNNING_TASKS[taskAccountPubKey].child.kill('SIGTERM');
      await this.handleQsdmNativeTaskProcessExit(
        taskAccountPubKey,
        'local-task-stop',
        skipRemoveFromRunningTasks
      );
      return;
    }

    this.RUNNING_TASKS[taskAccountPubKey].child.kill('SIGTERM');
    // TO DO: find why sometimes kill() doesn't trigger the exit event and we have to emit it manually
    this.RUNNING_TASKS[taskAccountPubKey].child.emit('exit', null, null);

    this.stopSubmissionCheck(taskAccountPubKey);
    this.stopSubmissionFetching(taskAccountPubKey);

    delete this.RUNNING_TASKS[taskAccountPubKey];

    if (!skipRemoveFromRunningTasks) {
      await this.removeRunningTaskPubKey(taskAccountPubKey);
    }

    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      try {
        await submitQsdmTaskActionIntent({
          taskId: taskAccountPubKey,
          action: 'stop',
          payload: { reason: 'local-task-stop' },
        });
      } catch (error: any) {
        return throwDetailedError({
          detailed: error?.message || error,
          type: ErrorType.GENERIC,
        });
      }
      return;
    }

    await this.runTimers();

    try {
      if (this.nodePropagationInterval) {
        clearInterval(this.nodePropagationInterval);
      }
      const runningTasks = (await this.getStartedTasks()).filter((task) => {
        return !!this.RUNNING_TASKS[task.task_id];
      });
      const mainSystemAccount = await getMainSystemAccountKeypair();
      const curr_subDomain = await namespaceInstance.storeGet('subdomain');
      console.log('curr_subDomain', curr_subDomain);
      initialPropagation(
        runningTasks,
        ATTENTION_TASK_ID,
        namespaceInstance,
        mainSystemAccount,
        `http://${curr_subDomain}`,
        true
      ).then(() => {
        this.nodePropagationInterval = setInterval(
          () =>
            runPeriodic(
              runningTasks,
              namespaceInstance,
              mainSystemAccount,
              `http://${curr_subDomain}`,
              true
            ),
          600000
        );
      });
    } catch (error: any) {
      console.error(error.message);
    }
  }

  async handleQsdmNativeTaskProcessExit(
    taskAccountPubKey: string,
    reason = 'local-task-exit',
    skipRemoveFromRunningTasks?: boolean
  ) {
    if (!this.RUNNING_TASKS[taskAccountPubKey]) {
      return;
    }

    this.stopSubmissionCheck(taskAccountPubKey);
    this.stopSubmissionFetching(taskAccountPubKey);

    delete this.RUNNING_TASKS[taskAccountPubKey];

    const shouldRestartQsdmSystemTask =
      QSDM_TASK_RUNTIME_MODE === 'qsdm-native' &&
      reason === 'qsdm-system-process-exit' &&
      !skipRemoveFromRunningTasks &&
      (isQsdmEdgeWorkerSystemTask(taskAccountPubKey) ||
        isQsdmMotherHiveSystemTask(taskAccountPubKey) ||
        isQsdmSkyFangLinkSystemTask(taskAccountPubKey));

    if (shouldRestartQsdmSystemTask) {
      await this.addRunningTaskPubKey(taskAccountPubKey);
      console.warn(
        `QSDM system task ${taskAccountPubKey} exited unexpectedly; keeping it marked as running and scheduling a restart.`
      );
      this.scheduleQsdmSystemTaskRestart(taskAccountPubKey, reason);
      return;
    }

    if (!skipRemoveFromRunningTasks) {
      await this.removeRunningTaskPubKey(taskAccountPubKey);
    }

    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      try {
        await submitQsdmTaskActionIntent({
          taskId: taskAccountPubKey,
          action: 'stop',
          payload: { reason },
        });
      } catch (error) {
        console.error('ERROR SUBMITTING QSDM TASK STOP', error);
      }
    }
  }

  async fetchAllTaskIds() {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      const tasks = await this.fetchQsdmNativeTasks();
      this.allTaskPubkeys = tasks
        .filter((task) => task.is_allowlisted)
        .map((task) => task.task_id);
      this.kplTaskPubKeys = [];
      this.privateTaskPubKeys = tasks
        .filter((task) => !task.is_allowlisted)
        .map((task) => task.task_id);
      return;
    }

    try {
      const getTaskPubkeys = async (
        programId: string | PublicKey,
        filters: MemcmpFilter[],
        dataSlice?: { offset: number; length: number }
      ) => {
        try {
          const url = `${SERVER_URL}/get-program-accounts?programId=${programId}&dataSlice_offset=${
            dataSlice?.offset || 0
          }&dataSlice_length=${dataSlice?.length || 0}&memcmp_offset=${
            filters[0]?.memcmp?.offset || 0
          }&memcmp_bytes=${filters[0].memcmp?.bytes || ''}`;
          const response = await fetch(url);
          console.log({ url });

          if (!response.ok) {
            throw new Error('Failed to fetch task accounts from cache server');
          }

          const accounts = (await response.json()) as {
            pubkey: PublicKey;
            account: AccountInfo<Buffer>;
          }[];

          return accounts.map(({ pubkey }) => pubkey as unknown as string);
        } catch (error) {
          console.error(error);
          const accounts = await sdk.k2Connection.getProgramAccounts(
            new PublicKey(programId),
            {
              dataSlice,
              filters,
            }
          );

          return accounts.map(({ pubkey }) => pubkey.toBase58());
        }
      };

      const networkUrl = getK2NetworkUrl();
      const networkFlag =
        networkUrl === MAINNET_RPC_URL
          ? 'mainnet'
          : networkUrl === TESTNET_RPC_URL
          ? 'testnet'
          : 'devnet';
      const privateTasksUrl = `${SERVER_URL}/get-eligible-tasks?is_allowlisted=false&endpoint=${networkFlag}`;

      console.log('privateTasksUrl', privateTasksUrl, getK2NetworkUrl());

      const results = await Promise.allSettled([
        getTaskPubkeys(
          process.env.TASK_CONTRACT_ID || TASK_CONTRACT_ID,
          getProgramAccountFilter(),
          {
            offset: 0,
            length: 0,
          }
        ),
        getTaskPubkeys(KPL_CONTRACT_ID, [
          {
            memcmp: {
              offset: 0,
              bytes: '5S',
            },
          },
        ]),
        fetch(privateTasksUrl, {
          method: 'GET',
        }).then((response) => response.json() as Promise<string[]>),
      ]);

      console.log('task ids', results);

      // Handle each result individually, preserving existing values if a fetch fails
      if (results[0].status === 'fulfilled') {
        this.allTaskPubkeys = results[0].value;
      }

      if (results[1].status === 'fulfilled') {
        this.kplTaskPubKeys = results[1].value.filter(
          (taskPubKey) =>
            ![
              'BAXsyoApqUjrRBXF8DdrcZrn6AMiCjdffTKgPf3AQW6w',
              'FPfjumESueM9WpTQT1nHrMQdTCzigh95JAS7Q48gop4y',
            ].includes(taskPubKey)
        );
      }

      if (results[2].status === 'fulfilled') {
        this.privateTaskPubKeys = results[2].value;
      } else {
        console.error('Failed to fetch private tasks:', results[2].reason);
        // Keep existing privateTaskPubKeys if fetch fails
      }
    } catch (err) {
      console.error('Error in fetchAllTaskIds:', err);
    }
  }

  public async addRunningTaskPubKey(pubkey: string) {
    const currentlyRunningTaskIds: Array<string> = Array.from(
      new Set([...(await this.getRunningTaskPubKeys()), pubkey])
    );
    await namespaceInstance.storeSet(
      SystemDbKeys.RunningTasks,
      JSON.stringify(currentlyRunningTaskIds)
    );
  }

  public async getIsTaskRunning(pubkey: string) {
    const isTaskRunning = !!this.RUNNING_TASKS[pubkey];
    return isTaskRunning;
  }

  /**
   * @dev store running tasks
   */
  private async removeRunningTaskPubKey(pubkey: string) {
    const currentlyRunningTaskIds: Array<string> =
      await this.getRunningTaskPubKeys();
    const isTaskRunning = currentlyRunningTaskIds.includes(pubkey);

    if (isTaskRunning) {
      const actualRunningTaskIds = currentlyRunningTaskIds.filter(
        (taskPubKey) => {
          return taskPubKey !== pubkey;
        }
      );

      await namespaceInstance.storeSet(
        SystemDbKeys.RunningTasks,
        JSON.stringify(actualRunningTaskIds)
      );
    } else {
      /**
       * @dev we cant throw error here, because it iwll interrupt the stopTask process
       */
      console.error(`Task ${pubkey} is not running`);
    }
  }

  async getRunningTaskPubKeys(): Promise<string[]> {
    const runningTasksStr: string | undefined =
      await namespaceInstance.storeGet(SystemDbKeys.RunningTasks);
    try {
      if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
        return runningTasksStr
          ? (JSON.parse(runningTasksStr) as Array<string>)
          : [];
      }

      const startedTasks = await this.getStartedTasksPubKeys();
      const runningTasks = (
        runningTasksStr ? (JSON.parse(runningTasksStr) as Array<string>) : []
      ).filter((task) => startedTasks.includes(task));

      return runningTasks;
    } catch (e) {
      return [];
    }
  }

  async getStartedTasksPubKeys(): Promise<string[]> {
    const files = fs.readdirSync(`${getAppDataPath()}/namespace`, {
      withFileTypes: true,
    });

    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      return files
        .filter((item) => {
          if (!item.isDirectory()) return false;
          if (this.allTaskPubkeys.length === 0) return true;
          return this.allTaskPubkeys.includes(item.name);
        })
        .map((item) => item.name);
    }

    const startedTasksPubKeys = files
      .filter((item) => item.isDirectory() && item.name.length > 40)
      /**
       * @dev we are using the name of the directory as the task pubkey
       */
      .map((item) => item.name);
    return startedTasksPubKeys;
  }

  removeTaskFromStartedTasks(taskPubKey: string) {
    // if is running, stop it
    if (this.RUNNING_TASKS[taskPubKey]) {
      this.RUNNING_TASKS[taskPubKey].child.kill();
      delete this.RUNNING_TASKS[taskPubKey];
    }

    // remove from started tasks data
    this.startedTasksData = this.startedTasksData?.filter(
      (task) => task.task_id !== taskPubKey
    );

    // remove from filesystem
    fs.rmdirSync(`${getAppDataPath()}/namespace/${taskPubKey}`, {
      recursive: true,
    });
  }

  async fetchTasksData(
    pubkeys: string[],
    options?: K2TasksDataFetchOptions
  ): Promise<(RawTaskData | null)[]> {
    const taskDataPromises = pubkeys.map(async (pubkey) => {
      try {
        const taskData = await this.getTaskState(pubkey, options);

        if (!taskData) {
          return null;
        }

        return taskData;
      } catch (error) {
        // handleTaskNotFoundError(pubkey, error);
        return null;
      }
    });

    return Promise.all(taskDataPromises);
  }

  async findLatestTaskVersion(taskId: string): Promise<string | null> {
    const task = await this.getTaskState(taskId, {
      withAvailableBalances: false,
    });

    if (task.migrated_to) {
      return this.findLatestTaskVersion(task.migrated_to);
    }
    if (!task.is_active) {
      return null;
    }
    return taskId;
  }

  async fetchStartedTaskData(
    isInitializingNode?: boolean
  ): Promise<{ upgradeableTaskIds: string[] }> {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      const startedTasksPubKeys = await this.getStartedTasksPubKeys();
      if (startedTasksPubKeys.length === 0) {
        this.startedTasksData = [];
        return { upgradeableTaskIds: [] };
      }

      const results = await Promise.allSettled(
        startedTasksPubKeys.map((pubkey) => this.getQsdmNativeTaskState(pubkey))
      );

      this.startedTasksData = results
        .filter(
          (result): result is PromiseFulfilledResult<RawTaskData> =>
            result.status === 'fulfilled'
        )
        .map((result) => result.value);

      return { upgradeableTaskIds: [] };
    }

    const upgradeableTaskIds: string[] = [];
    const startedTasksPubKeys: Array<string> =
      await this.getStartedTasksPubKeys();

    if (startedTasksPubKeys.length === 0) {
      this.startedTasksData = [];
      return { upgradeableTaskIds: [] };
    }

    const fetchTaskWithTimeout = async (pubkey: string) => {
      const TIMEOUT_MS = 30000; // 30 seconds timeout

      try {
        // eslint-disable-next-line promise/param-names
        const timeoutPromise = new Promise((_, reject) => {
          setTimeout(() => reject(new Error('Timeout')), TIMEOUT_MS);
        });

        const taskPromise = (async () => {
          const existingStateFromClass = this.startedTasksData?.find(
            (task) => task.task_id === pubkey
          );
          const existingStateFromCache = await getCompleteTaskFromCache(pubkey);
          const existingState =
            existingStateFromClass || existingStateFromCache;

          const taskData = await this.getTaskState(pubkey, {
            withAvailableBalances: true,
          });

          if (taskData.migrated_to) {
            const newTaskVersionId = await this.findLatestTaskVersion(
              taskData.migrated_to
            );
            if (newTaskVersionId) {
              upgradeableTaskIds.push(newTaskVersionId);
              taskData.migrated_to = newTaskVersionId;
            }
          }

          if (isInitializingNode) {
            await getMyTaskStake({} as Event, {
              taskAccountPubKey: pubkey,
              revalidate: true,
              taskType: taskData.task_type || existingState?.task_type,
            });
          }

          const isKPLTask = !!taskData.token_type;
          return {
            ...taskData,
            task_type: isKPLTask ? 'KPL' : 'CELL',
            submissions: existingStateFromCache?.submissions,
          };
        })();

        return await Promise.race([taskPromise, timeoutPromise]);
      } catch (error) {
        console.log(
          `Timeout or error fetching task ${pubkey}, falling back to cache`
        );
        const cachedData = await getCompleteTaskFromCache(pubkey);
        if (cachedData) {
          const isKPLTask = !!cachedData.token_type;
          return {
            ...cachedData,
            task_type: isKPLTask ? 'KPL' : 'CELL',
          };
        }
        throw error;
      }
    };

    const results = await Promise.allSettled(
      startedTasksPubKeys.map((pubkey) => fetchTaskWithTimeout(pubkey))
    );

    const filteredResults = results.filter(
      (result) => result.status === 'fulfilled'
    ) as PromiseFulfilledResult<RawTaskData>[];

    const promisesData = filteredResults.map((result) => result.value);

    if (promisesData.length === 0) {
      this.startedTasksData = null;
    } else {
      this.startedTasksData = promisesData;
      await saveBaseStatesToCache(promisesData);
    }

    return { upgradeableTaskIds };
  }

  async getTaskMetadataUtil(metadataCID: string): Promise<TaskMetadata> {
    if (this.taskMetadata[metadataCID]) {
      return this.taskMetadata[metadataCID];
    }
    const taskMetadata = await getTaskMetadata({} as Event, {
      metadataCID,
    });
    this.taskMetadata[metadataCID] = taskMetadata;
    return taskMetadata;
  }
}
