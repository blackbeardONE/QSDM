import { ChildProcess, fork, ForkOptions } from 'child_process';
import { dialog, Event } from 'electron';
import * as fsSync from 'fs';
import { Transform } from 'stream';

import detectPort from 'detect-port';
import * as rfs from 'rotating-file-stream';

import { PublicKey } from 'vendor/qsdm-chain/web3';
import { Keypair } from 'vendor/qsdm-chain/web3';
import {
  ITaskNodeBase,
  LogLevel,
  TaskData as TaskNodeTaskData,
} from 'vendor/qsdm-chain/taskNode';
import { RendererEndpoints } from 'config/endpoints';
import { SERVER_PORT } from 'config/node';
import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { SystemDbKeys } from 'config/systemDbKeys';
import cryptoRandomString from 'crypto-random-string';
import { Application } from 'express';
import { get } from 'lodash';
import getUserConfig from 'main/controllers/getUserConfig';
import db from 'main/db';
import { getK2NetworkUrl } from 'main/node/helpers/k2NetworkUrl';
import { Namespace, namespaceInstance } from 'main/node/helpers/Namespace';
import { assertQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import qsdmHiveTasks from 'main/services/qsdmHiveTasks';
import {
  enrollQsdmMiner,
  getQsdmMinerEnrollmentStatus,
  prepareQsdmMinerV2Config,
} from 'main/services/qsdmMinerEnrollment';
import {
  assertQsdmMotherHiveConfigured,
  isQsdmEdgeWorkerSystemTask,
  isQsdmMinerSystemTask,
  isQsdmMotherHiveSystemTask,
  isQsdmSkyFangLinkSystemTask,
  isQsdmSystemTask,
  requireQsdmSkyFangWalletLinkedForSkyFangLink,
  startQsdmEdgeWorkerSystemProcess,
  startQsdmMotherHiveSystemProcess,
  startQsdmSkyFangLinkSystemProcess,
  startOrAdoptQsdmMinerSystemProcess,
} from 'main/services/qsdmSystemTasks';
import { getQsdmTaskStakeOwnership } from 'main/services/qsdmTaskStake';
import { updateTaskCacheRecord } from 'main/services/tasks-cache-utils';
import { sleep } from 'main/util';
import { ErrorType, TaskRetryData } from 'models';
import { TaskStartStopParam } from 'models/api';
import { TaskNotificationPayloadType } from 'preload/apis/tasks/onTaskNotificationReceived';
import { MAINNET_RPC_URL } from 'renderer/features/shared/constants';
import { throwDetailedError } from 'utils';
import { sendEventAllWindows } from 'utils/sendEventAllWindows';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';

import {
  forceKillChildProcess,
  getMainSystemAccountKeypair,
  getStakingAccountKeypair,
} from '../node/helpers';
import { getAppDataPath } from '../node/helpers/getAppDataPath';
import initExpressApp from '../node/initExpressApp';

import getStakingAccountPublicKey from './getStakingAccountPubKey';
import { getTaskSource } from './getTaskSource';
import retryTask from './retryTask';
import storeUserConfig from './storeUserConfig';
import { getMyTaskStake } from './tasks';
import { getTaskPairedVariablesNamesWithValues } from './taskVariables';

const OPERATION_MODE = 'service';
const logTimestampFormat: Intl.DateTimeFormatOptions = {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
  year: 'numeric',
  hour: 'numeric',
  minute: 'numeric',
  second: 'numeric',
  hour12: true,
};
const MAX_PORT_EXPOSURE_RETRIES = 3;

const formatCellAmount = (amount: number) =>
  Number.isInteger(amount) ? String(amount) : amount.toFixed(3);

const compactAddress = (address: string) =>
  address.length > 20
    ? `${address.slice(0, 8)}...${address.slice(-6)}`
    : address;

const buildMissingQsdmStakeMessage = async (taskId: string, sender: string) => {
  try {
    const ownership = await getQsdmTaskStakeOwnership(taskId, sender);
    const currentSigner = ownership.sender
      ? compactAddress(ownership.sender)
      : 'the active signer';

    if (ownership.foreignParticipants.length > 0) {
      const foreignOwners = ownership.foreignParticipants
        .slice(0, 3)
        .map(
          (participant) =>
            `${compactAddress(participant.sender)} (${formatCellAmount(
              participant.stakeCell
            )} CELL${participant.running ? ', running' : ''})`
        )
        .join(', ');

      return `Can't start task ${taskId}: active QSDM signer ${currentSigner} has 0 CELL staked on this task. Existing stake is bound to another signer: ${foreignOwners}. QSDM task stake is wallet-bound; import the signer wallet that owns that stake, or add stake with the current signer.`;
    }

    return `Can't start task ${taskId}: active QSDM signer ${currentSigner} has no CELL stake on this task. Add stake with the current signer before starting.`;
  } catch (error: any) {
    console.error('Error while explaining missing native QSDM stake', error);
    return `Can't start task ${taskId}, because the active QSDM signer has no confirmed stake on this task.`;
  }
};

const startTask = async (
  _: Event,
  payload: TaskStartStopParam
): Promise<void> => {
  const { taskAccountPubKey, isPrivate, forceRefetch } = payload;
  const isQsdmNativeTaskRuntime = QSDM_TASK_RUNTIME_MODE === 'qsdm-native';
  const isSystemTask = isQsdmSystemTask(taskAccountPubKey);
  const isSystemMinerTask = isQsdmMinerSystemTask(taskAccountPubKey);
  const isSystemEdgeWorkerTask = isQsdmEdgeWorkerSystemTask(taskAccountPubKey);
  const isSystemMotherHiveTask = isQsdmMotherHiveSystemTask(taskAccountPubKey);
  const isSystemSkyFangLinkTask =
    isQsdmSkyFangLinkSystemTask(taskAccountPubKey);

  if (isQsdmNativeTaskRuntime) {
    try {
      await assertQsdmCanonicalChainSafety();
    } catch (error: any) {
      return throwDetailedError({
        detailed: error?.message || error,
        type: ErrorType.TASK_START,
      });
    }
  }

  const stakingAccKeypair = isQsdmNativeTaskRuntime
    ? null
    : await getStakingAccountKeypair();
  const qsdmTaskActionSender = isQsdmNativeTaskRuntime
    ? getQsdmTaskActionSender()
    : '';
  const stakingPubkey =
    stakingAccKeypair?.publicKey.toBase58() || qsdmTaskActionSender || 'qsdm';
  const isTaskRunning = await qsdmHiveTasks.getIsTaskRunning(taskAccountPubKey);

  console.log({ isTaskRunning });

  if (isTaskRunning) {
    if (isQsdmNativeTaskRuntime && isSystemTask) {
      const reconciled = await qsdmHiveTasks.reconcileQsdmSystemTaskRuntime(
        taskAccountPubKey,
        { submitStartAction: false }
      );
      if (reconciled) {
        console.log(
          'System task is already running; treating start as idempotent.'
        );
        return;
      }
      console.log(
        'System task is marked running, but the local runtime is absent; starting it again.'
      );
    } else {
      console.log('Task is already running; treating start as idempotent.');
      return;
    }
  }
  const mainSystemAccount = await getMainSystemAccountKeypair();

  const taskInfo = await qsdmHiveTasks.getTaskState(taskAccountPubKey);

  if (!taskInfo) {
    console.error(`Task ${taskAccountPubKey} doesn't exist`);
    return throwDetailedError({
      detailed: 'Task not found',
      type: ErrorType.TASK_NOT_FOUND,
    });
  }

  let taskStakeInfo: number | null = null;
  try {
    taskStakeInfo = await getMyTaskStake({} as Event, {
      taskAccountPubKey,
      revalidate: isQsdmNativeTaskRuntime,
      taskType: taskInfo.token_type ? 'KPL' : 'CELL',
    });
  } catch (error) {
    taskStakeInfo = 0;
  }

  if (isQsdmNativeTaskRuntime && isSystemMinerTask) {
    try {
      await prepareQsdmMinerV2Config();
      let enrollment = await getQsdmMinerEnrollmentStatus();
      if (!enrollment.ready) {
        if (enrollment.error) throw new Error(enrollment.error);
        const required = enrollment.requiredStakeCell;
        const available = enrollment.balanceCell || 0;
        const canPrepay = available >= required + 0.001;
        const canBondFromRewards = enrollment.deferredBondAvailable;
        if (!canPrepay && !canBondFromRewards) {
          throw new Error(
            `QSDM NVIDIA mining requires a ${required} CELL enrollment bond plus a 0.001 CELL transaction fee. This signer currently has ${available} CELL.`
          );
        }
        const buttons = canBondFromRewards
          ? canPrepay
            ? ['Use mining earnings', 'Lock CELL now', 'Cancel']
            : ['Use mining earnings', 'Cancel']
          : ['Lock CELL now', 'Cancel'];
        const cancelId = buttons.length - 1;
        const confirmation = await dialog.showMessageBox({
          type: 'warning',
          title: 'Enroll this NVIDIA miner?',
          message: canBondFromRewards
            ? `Choose how to build the ${required} CELL protocol mining bond.`
            : `Lock ${required} CELL as the protocol mining bond?`,
          detail:
            `GPU: ${enrollment.gpu?.name || 'NVIDIA GPU'}\n` +
            `Node: ${enrollment.nodeId || 'QSDM Hive miner'}\n\n` +
            (canBondFromRewards
              ? 'Use mining earnings starts at 0 CELL. Your protocol rewards fill the locked bond first; only rewards above the target become spendable.\n\n'
              : '') +
            'The bond is slashable for invalid proofs. Unenrollment starts a 7-day unbonding period; it is not an extra Hive task stake.',
          buttons,
          defaultId: 0,
          cancelId,
          noLink: true,
        });
        if (confirmation.response === cancelId) {
          throw new Error(
            'Miner enrollment was cancelled. No CELL was locked.'
          );
        }
        const bondMode =
          canBondFromRewards && confirmation.response === 0
            ? 'mining_rewards'
            : 'upfront';
        enrollment = await enrollQsdmMiner(bondMode);
        if (!enrollment.ready) {
          throw new Error('Miner enrollment did not become active.');
        }
      }
    } catch (error: any) {
      return throwDetailedError({
        detailed: error?.message || error,
        type: ErrorType.TASK_START,
      });
    }
  }

  // if stake is undefined or 0 -> stop
  if ((!taskStakeInfo || taskStakeInfo === 0) && !isSystemMinerTask) {
    console.log("Can't start task, because it is not staked");
    const detailed = isQsdmNativeTaskRuntime
      ? await buildMissingQsdmStakeMessage(
          taskAccountPubKey,
          qsdmTaskActionSender
        )
      : `Can't start task ${taskAccountPubKey}, because it is not staked`;

    return throwDetailedError({
      detailed,
      type: ErrorType.TASK_START,
    });
  }

  if (isQsdmNativeTaskRuntime && isSystemSkyFangLinkTask) {
    try {
      await requireQsdmSkyFangWalletLinkedForSkyFangLink();
    } catch (error: any) {
      console.warn(
        'Starting QSDM Sky Fang Link verifier before live wallet-link verification is ready. The verifier will keep checking and will only submit after the active QSDM wallet is linked.',
        error
      );
    }
  }

  if (isQsdmNativeTaskRuntime && isSystemMotherHiveTask) {
    try {
      assertQsdmMotherHiveConfigured();
    } catch (error: any) {
      return throwDetailedError({
        detailed: error?.message || error,
        type: ErrorType.TASK_START,
      });
    }
  }

  let qsdmStartActionAccepted = false;
  if (
    isQsdmNativeTaskRuntime &&
    !isSystemMinerTask &&
    !(isSystemTask && isTaskRunning)
  ) {
    try {
      await submitQsdmTaskActionIntent({
        taskId: taskAccountPubKey,
        action: 'start',
        payload: {
          mode: isSystemMinerTask
            ? 'qsdm-system-miner'
            : isSystemEdgeWorkerTask
            ? taskAccountPubKey
            : isSystemMotherHiveTask
            ? 'qsdm-hive-mother'
            : isSystemSkyFangLinkTask
            ? 'qsdm-skyfang-wallet-link'
            : OPERATION_MODE,
          isPrivate: Boolean(isPrivate),
          forceRefetch: Boolean(forceRefetch),
          ...(isSystemTask ? { no_expiry: true, local_process: true } : {}),
        },
      });
      qsdmStartActionAccepted = true;
    } catch (error: any) {
      return throwDetailedError({
        detailed: error?.message || error,
        type: ErrorType.TASK_START,
      });
    }
  }

  await updateTaskCacheRecord(taskAccountPubKey, taskInfo);

  console.log('STARTED TASK DATA', taskInfo?.task_name);

  const userConfig = await getUserConfig();

  try {
    if (isSystemTask) {
      const namespace = new Namespace({
        taskTxId: taskAccountPubKey,
        serverApp: null as any,
        mainSystemAccount,
        db,
        taskType: 'CELL',
        rpcUrl: getK2NetworkUrl(),
        taskData: taskInfo,
      });
      const { child, secret } = isSystemEdgeWorkerTask
        ? startQsdmEdgeWorkerSystemProcess(taskAccountPubKey, taskInfo)
        : isSystemMotherHiveTask
        ? startQsdmMotherHiveSystemProcess()
        : isSystemSkyFangLinkTask
        ? startQsdmSkyFangLinkSystemProcess(taskInfo)
        : await startOrAdoptQsdmMinerSystemProcess();

      child.once('exit', async (code, signal) => {
        console.error(
          `${taskInfo.task_name} task process ${child.pid} exited with code ${code} and signal ${signal}`
        );
        await qsdmHiveTasks.handleQsdmNativeTaskProcessExit(
          taskAccountPubKey,
          'qsdm-system-process-exit',
          undefined,
          child
        );
      });

      await qsdmHiveTasks.startTask(
        taskAccountPubKey,
        namespace,
        child,
        0,
        secret,
        { ...taskInfo, stake_list: { [stakingPubkey]: taskStakeInfo || 0 } }
      );
      await storeUserConfig({} as Event, {
        settings: { ...userConfig, hasRunFirstTask: true },
      });
      console.log('QSDM SYSTEM TASK STARTED:', taskAccountPubKey);
      return;
    }

    const expressApp = await initExpressApp();
    let portExposure = await namespaceInstance.storeGet('Port_Exposure');
    console.log('port_exposure', portExposure);

    let numberOfPortExposureRetries = 0;

    if (!userConfig?.networkingFeaturesEnabled) {
      while (
        portExposure === 'Pending' &&
        numberOfPortExposureRetries < MAX_PORT_EXPOSURE_RETRIES
      ) {
        numberOfPortExposureRetries += 1;
        await sleep(2000);
        portExposure = await namespaceInstance.storeGet('Port_Exposure');
      }
    }

    console.log('LOADING TASK:', taskAccountPubKey);
    await loadTask({
      taskAuditProgram: taskInfo.task_audit_program,
      taskId: taskAccountPubKey,
    });

    await clearTaskRetryTimeout(taskAccountPubKey);
    const { namespace, child, expressAppPort, secret } = await executeTasks(
      { ...taskInfo, task_id: taskAccountPubKey },
      expressApp,
      OPERATION_MODE,
      mainSystemAccount
    );

    await qsdmHiveTasks.startTask(
      taskAccountPubKey,
      namespace,
      child,
      expressAppPort,
      secret,
      { ...taskInfo, stake_list: { [stakingPubkey]: taskStakeInfo || 0 } }
    );
    await storeUserConfig({} as Event, {
      settings: { ...userConfig, hasRunFirstTask: true },
    });
    console.log('TASK STARTED:', taskAccountPubKey);
  } catch (err: any) {
    console.error('ERROR STARTING TASK', err);
    if (
      isQsdmNativeTaskRuntime &&
      (qsdmStartActionAccepted || (isSystemTask && isTaskRunning))
    ) {
      try {
        await submitQsdmTaskActionIntent({
          taskId: taskAccountPubKey,
          action: 'stop',
          payload: {
            reason: 'local-start-failed',
            error: String(err?.message || err).slice(0, 500),
          },
        });
      } catch (stopError) {
        console.error('ERROR SUBMITTING QSDM TASK STOP', stopError);
      }
    }
    return throwDetailedError({
      detailed: err,
      type: ErrorType.TASK_START,
    });
  }
};

export async function clearTaskRetryTimeout(taskPubkey: string) {
  const allTaskRetryData: {
    [key: string]: TaskRetryData;
  } = (await namespaceInstance.storeGet(SystemDbKeys.TaskRetryData)) || {};

  const taskRetryData = get(allTaskRetryData, taskPubkey, null);
  if (taskRetryData?.timerReference) {
    clearTimeout(taskRetryData?.timerReference); // clear ongoing retry timerReference
  }

  if (taskRetryData) {
    taskRetryData.timerReference = null;
  }

  const payload: any = {
    ...allTaskRetryData,
    [taskPubkey]: taskRetryData,
  };

  namespaceInstance.storeSet(SystemDbKeys.TaskRetryData, payload);
}

/**
 * Load tasks and generate task executables
 * @param {any[]} selectedTasks Array of selected tasks
 * @param {any} expressApp
 * @returns {any[]} Array of executable tasks
 */
// bafybeiabhpqrknrzz5fka2otptaw7lubz7awh5f7n2kruk52akq4jr5dh4
export async function loadTask({
  taskAuditProgram,
  taskId,
}: {
  taskAuditProgram: string;
  taskId: string;
}) {
  const executablesDirectoryPath = `${getAppDataPath()}/executables`;
  const presumedSourceCodePath = `${executablesDirectoryPath}/${taskAuditProgram}.js`;
  let shouldDownloadExecutable = !fsSync.existsSync(presumedSourceCodePath);

  if (!shouldDownloadExecutable) {
    const fileContent = fsSync.readFileSync(presumedSourceCodePath, 'utf8');
    shouldDownloadExecutable = fileContent.startsWith('<');
  }

  /**
   * 1.  start node
   * 2. Check the executable folders, and there is a file with task_audit_prgram name
   * 3. If task with this task_audit_prgram is not hashed, force download and alternate malicious executable code
   */

  if (shouldDownloadExecutable) {
    const sourceCode: string = await getTaskSource({} as Event, {
      taskAuditProgram,
    });
    /**
     * @dev save record in task_id -> taskAuditProgram map in db fpr the first time when task is loaded
     * it will be used for tracking the task and its executable
     */
    await saveTaskAuditProgramIdToTaskIdMap(taskId, taskAuditProgram);
    fsSync.mkdirSync(executablesDirectoryPath, { recursive: true });
    fsSync.writeFileSync(presumedSourceCodePath, sourceCode);
  }
}

async function saveTaskAuditProgramIdToTaskIdMap(
  taskId: string,
  taskAuditProgram: string
) {
  let currentMap: Record<string, string>;

  try {
    const currentMapString = await db.get(
      SystemDbKeys.TaskIdToAuditProgramIdMap
    );
    currentMap = JSON.parse(currentMapString as string) as Record<
      string,
      string
    >;
  } catch (error) {
    currentMap = {};
  }

  currentMap[taskAuditProgram] = taskId;

  await db.put(
    SystemDbKeys.TaskIdToAuditProgramIdMap,
    JSON.stringify(currentMap)
  );
}

/**
 * Initializes and executes tasks
 * @param {any[]} selectedTask Array of selected tasks
 * @param {any[]} executableTasks Array of executable tasks
 */
export async function executeTasks(
  selectedTask: Required<TaskNodeTaskData> & {
    stake_list: Record<string, number>;
    token_type?: PublicKey;
  },
  expressApp: Application,
  operationMode: string,
  mainSystemAccount: Keypair
): Promise<{
  namespace: ITaskNodeBase;
  child: ChildProcess;
  expressAppPort: number;
  secret: string;
}> {
  const availablePort = await detectPort();

  const secret = cryptoRandomString({ length: 20 });
  const options: ForkOptions = {
    env: await getTaskPairedVariablesNamesWithValues({} as Event, {
      taskAccountPubKey: selectedTask.task_id,
    }),
    silent: true,
  };
  if (options.env === undefined) options.env = {};
  options.env.PATH = process.env.PATH;

  const stakingAccPubkey =
    QSDM_TASK_RUNTIME_MODE === 'qsdm-native'
      ? getQsdmTaskActionSender() || 'qsdm'
      : await getStakingAccountPublicKey();
  const STAKE = selectedTask.stake_list[stakingAccPubkey];
  fsSync.mkdirSync(`${getAppDataPath()}/namespace/${selectedTask.task_id}`, {
    recursive: true,
  });
  let logFile;
  try {
    logFile = rfs.createStream('task.log', {
      size: '5M', // Maximum file size
      compress: 'gzip', // Compress rotated files using gzip
      path: `${getAppDataPath()}/namespace/${selectedTask.task_id}`, // Directory path for log files
    });

    // Event listener for the rotation event
    logFile.on('rotated', (filename) => {
      // Delete the previous log file when rotation occurs
      if (filename.includes('log.gz')) {
        fsSync.unlink(filename, (err) => {
          if (err) {
            console.error(`Error deleting log file ${filename}:`, err);
          } else {
            console.log(`Deleted log file ${filename}`);
          }
        });
      }
    });
    logFile.on('error', (error) => {
      console.error('ERROR IN TASK LOG listener', error);
    });
  } catch (error) {
    console.error('ERROR IN TASK LOG', error);
  }
  const childTaskProcess = fork(
    `${getAppDataPath()}/executables/${selectedTask.task_audit_program}.js`,
    [
      `${selectedTask.task_name}`,
      `${selectedTask.task_id}`,
      `${availablePort}`,
      `${operationMode}`,
      `${mainSystemAccount.publicKey.toBase58()}`,
      `${secret}`,
      `${getK2NetworkUrl() || MAINNET_RPC_URL}`,
      `${process.env.SERVICE_URL}`,
      `${STAKE}`,
      `${SERVER_PORT}`,
    ],
    options
  );

  const messageTransform = (
    formatter: (
      timestamp: string,
      messageContent: string,
      isError: boolean
    ) => string
  ): Transform =>
    new Transform({
      transform(data, encoding, callback) {
        try {
          const timestamp = new Date().toLocaleString(
            'en-US',
            logTimestampFormat
          );
          const isErrorMessage = data
            .toString()
            .toLowerCase()
            .includes('error');
          const message = formatter(timestamp, data.toString(), isErrorMessage);
          // Check if the stream is still writable before pushing data
          if (!this.writableEnded) {
            this.push(message);
            callback();
          } else {
            // Handle the situation when the stream is no longer writable
            const error = new Error(
              'Attempted to write after stream has ended'
            );
            console.error(error);
            callback(error);
          }
        } catch (error: any) {
          // Handle other unexpected errors
          console.error('Error in transform stream:', error);
          callback(error);
        }
      },
    });

  if (logFile) {
    childTaskProcess.stdout
      ?.pipe(
        messageTransform((timestamp, message) => `[${timestamp}] ${message}`)
      )
      .pipe(logFile)
      .on('error', (err) => {
        // Handle the error here
        console.error('Error in stream pipeline:', err);
        // Depending on your application, you might want to clean up resources or shut down the process
      });

    childTaskProcess.stderr
      ?.pipe(
        messageTransform((timestamp, message, isError) => {
          const label = isError ? 'ERROR' : 'WARNING';
          return `[${timestamp}] ${label}: ${message}`;
        })
      )
      .pipe(logFile)
      .on('error', (err) => {
        // Handle the error here
        console.error('Error in stream pipeline:', err);
        // Depending on your application, you might want to clean up resources or shut down the process
      });
  }

  childTaskProcess.on('error', async (err) => {
    console.error('Error starting child process:', err);
    qsdmHiveTasks.stopTask(selectedTask.task_id, true);
  });

  childTaskProcess.on('exit', async (code, signal) => {
    console.error(
      `Child process ${childTaskProcess.pid} for task ${namespace.taskData.task_name} (${namespace.taskData.task_id}) exited with code ${code} and signal ${signal}`
    );
    childTaskProcess.removeAllListeners();
    const shouldRetry = code === 0;
    const taskHadInnerLogicError = code === 1;

    await sleep(2000);

    forceKillChildProcess(childTaskProcess);

    if (shouldRetry) {
      retryTask(
        selectedTask,
        expressApp,
        OPERATION_MODE,
        mainSystemAccount as any,
        executeTasks
      );
    }

    if (taskHadInnerLogicError) {
      qsdmHiveTasks.stopTask(selectedTask.task_id);
    }
  });

  const taskType = selectedTask.token_type ? 'KPL' : 'CELL';

  const namespace = new Namespace({
    taskTxId: selectedTask.task_id,
    serverApp: expressApp as any,
    mainSystemAccount,
    db,
    taskType,
    rpcUrl: getK2NetworkUrl(),
    taskData: selectedTask,
  });

  namespace.setLoggerCallback(
    (level: LogLevel, message: string, action: string) => {
      const payload = {
        level,
        message,
        action,
        taskId: selectedTask.task_id,
        taskName: selectedTask.task_name,
      } as TaskNotificationPayloadType;

      sendEventAllWindows(
        RendererEndpoints.TASK_NOTIFICATION_RECEIVED,
        payload
      );
    }
  );

  return {
    namespace,
    child: childTaskProcess,
    expressAppPort: availablePort,
    secret,
  };
}

export default startTask;
