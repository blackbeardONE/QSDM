import { Event } from 'electron';
import { promises as fsPromises } from 'fs';

import { buildQsdmTaskReadUrls, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { getAppDataPath } from 'main/node/helpers/getAppDataPath';
import {
  getQsdmSystemTaskMetadata,
  mergeQsdmSystemTasks,
} from 'main/services/qsdmSystemTasks';
import { qsdmGetFirstJson } from 'main/services/qsdmHttpRead';
import { ErrorType, GetTaskMetadataParam, TaskMetadata } from 'models';
import { QsdmTasksListResponse } from 'models/api/qsdm';
import { throwDetailedError } from 'utils';

import { fetchFromIPFSOrArweave } from './fetchFromIPFSOrArweave';

const { readFile, writeFile, access, mkdir } = fsPromises;
const METADATA_CACHE_DIR = `${getAppDataPath()}/metadata`;

const getQsdmNativeTaskMetadata = async (
  metadataCID: string
): Promise<TaskMetadata | null> => {
  if (QSDM_TASK_RUNTIME_MODE !== 'qsdm-native') {
    return null;
  }

  const systemTaskMetadata = getQsdmSystemTaskMetadata(metadataCID);
  if (systemTaskMetadata) {
    return systemTaskMetadata;
  }

  try {
    const response = await qsdmGetFirstJson<QsdmTasksListResponse>(
      buildQsdmTaskReadUrls('/tasks'),
      { timeout: 4000 }
    );
    const task = mergeQsdmSystemTasks(response.tasks || []).find(
      (candidate) =>
        candidate.task_metadata === metadataCID ||
        candidate.task_id === metadataCID
    );
    if (!task) {
      return null;
    }

    return {
      author: task.task_manager?.toString() || 'QSDM',
      description:
        task.task_description ||
        task.task_name ||
        'Native QSDM task registered by QSDM Core.',
      repositoryUrl: '',
      createdAt: 0,
      imageUrl: '',
      migrationDescription: '',
      requirementsTags: [],
      infoUrl: '',
      tags: ['qsdm-native', 'CELL'],
    };
  } catch (error) {
    console.error('Error fetching native QSDM task metadata', error);
    return null;
  }
};

export const getTaskMetadata = async (
  _: Event,
  payload: GetTaskMetadataParam
): Promise<TaskMetadata> => {
  // payload validation
  if (!payload?.metadataCID) {
    throw throwDetailedError({
      detailed: 'Get Task Metadata error: payload is not valid',
      type: ErrorType.GENERIC,
    });
  }

  const cacheFilePath = `${METADATA_CACHE_DIR}/${payload.metadataCID}.json`;

  try {
    await mkdir(METADATA_CACHE_DIR, { recursive: true });
    const nativeMetadata = await getQsdmNativeTaskMetadata(payload.metadataCID);
    if (nativeMetadata) {
      await writeFile(cacheFilePath, JSON.stringify(nativeMetadata), 'utf-8');
      return nativeMetadata;
    }

    await access(cacheFilePath);

    // If cache exists, read and return its content
    const cachedMetadata = await readFile(cacheFilePath, 'utf-8');
    return JSON.parse(cachedMetadata) as TaskMetadata;
  } catch (cacheError) {
    // Otherwise we fetch it and save it to cache
    try {
      const tooManyRequestsErrorRegex = /<[^>]*>429 Too Many Requests<[^>]*>/g;

      const result = await fetchFromIPFSOrArweave(
        payload.metadataCID,
        'metadata.json'
      );

      if (tooManyRequestsErrorRegex.test(result)) {
        return throwDetailedError({
          detailed: '429 Too Many Requests',
          type: ErrorType.TOO_MANY_REQUESTS,
        });
      }

      const metadata = JSON.parse(result) as TaskMetadata;
      const metadataWasCorrectlyFetched = !!metadata?.description;

      if (!metadataWasCorrectlyFetched) {
        throw new Error('Metadata was not correctly fetched');
      }

      await writeFile(cacheFilePath, JSON.stringify(metadata), 'utf-8');
      return metadata;
    } catch (e: any) {
      console.error(e);
      return throwDetailedError({
        detailed: e,
        type: ErrorType.NO_TASK_METADATA,
      });
    }
  }
};
