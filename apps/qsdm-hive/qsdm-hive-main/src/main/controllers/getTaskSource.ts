import { app, Event } from 'electron';

import { buildQsdmTaskReadUrls, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { getQsdmTaskRuntimeCompatibilityIssue } from 'main/services/qsdmTaskCatalogRuntime';
import { qsdmGetFirstJson } from 'main/services/qsdmHttpRead';
import { ErrorType, GetTaskSourceParam } from 'models';
import { QsdmTasksListResponse } from 'models/api/qsdm';
import { throwDetailedError } from 'utils';

import { fetchFromIPFSOrArweave } from './fetchFromIPFSOrArweave';

const buildNativeTaskSource = (taskId: string): string => `
const http = require('http');

const taskName = process.argv[2] || ${JSON.stringify(taskId)};
const taskId = process.argv[3] || ${JSON.stringify(taskId)};
const secret = process.argv[7] || '';
const serverPort = Number(process.argv[11] || process.env.SERVER_PORT || 30017);
let round = 0;

function postNamespace(method, ...params) {
  const body = JSON.stringify({
    taskId,
    secret,
    args: [method, ...params],
  });

  const request = http.request(
    {
      hostname: '127.0.0.1',
      port: serverPort,
      path: '/namespace-wrapper',
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(body),
      },
      timeout: 10000,
    },
    (response) => {
      let data = '';
      response.on('data', (chunk) => {
        data += chunk;
      });
      response.on('end', () => {
        console.log(
          '[qsdm-native-task]',
          taskName,
          method,
          response.statusCode,
          data
        );
      });
    }
  );

  request.on('error', (error) => {
    console.error('[qsdm-native-task]', method, error.message);
  });
  request.write(body);
  request.end();
}

function submitProof() {
  round += 1;
  const proof = 'qsdm-native-proof:' + taskId + ':' + Date.now();
  postNamespace('checkSubmissionAndUpdateRound', proof, round);
}

console.log('[qsdm-native-task] started', taskName, taskId);
submitProof();
const interval = setInterval(submitProof, 60000);

function shutdown() {
  clearInterval(interval);
  console.log('[qsdm-native-task] stopped', taskName, taskId);
  process.exit(0);
}

process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);
`;

const getQsdmNativeTaskSource = async (
  taskAuditProgram: string
): Promise<string | null> => {
  if (QSDM_TASK_RUNTIME_MODE !== 'qsdm-native') {
    return null;
  }

  let response: QsdmTasksListResponse;
  try {
    response = await qsdmGetFirstJson<QsdmTasksListResponse>(
      buildQsdmTaskReadUrls('/tasks'),
      { timeout: 4000 }
    );
  } catch (error) {
    console.warn(
      'Native QSDM task source is temporarily unavailable.',
      error instanceof Error ? error.message : String(error)
    );
    return null;
  }

  const task = (response.tasks || []).find(
    (candidate) =>
      candidate.task_audit_program === taskAuditProgram ||
      candidate.task_id === taskAuditProgram
  );
  if (!task) {
    return null;
  }
  const runtimeIssue = getQsdmTaskRuntimeCompatibilityIssue(
    task,
    app.getVersion()
  );
  if (runtimeIssue) {
    throw new Error(
      `QSDM catalog task ${task.task_id} cannot run safely: ${runtimeIssue}`
    );
  }
  if (task.manifest?.runtime.kind === 'capability') {
    return buildNativeTaskSource(task.task_id);
  }
  if (task.manifest) {
    throw new Error(
      `QSDM catalog task ${task.task_id} declares an unsupported runtime.`
    );
  }
  return buildNativeTaskSource(task.task_id);
};

export const getTaskSource = async (
  _: Event,
  { taskAuditProgram }: GetTaskSourceParam
): Promise<string> => {
  try {
    const nativeSource = await getQsdmNativeTaskSource(taskAuditProgram);
    if (nativeSource) {
      return nativeSource;
    }

    const sourceCode = await fetchFromIPFSOrArweave(
      taskAuditProgram,
      'main.js'
    );

    if (!sourceCode) {
      return throwDetailedError({
        detailed: `No Task source found of ID ${taskAuditProgram} to fetch`,
        type: ErrorType.NO_TASK_SOURCECODE,
      });
    }

    return sourceCode;
  } catch (e: any) {
    console.error(e);
    return throwDetailedError({
      detailed: e,
      type: ErrorType.NO_TASK_SOURCECODE,
    });
  }
};
