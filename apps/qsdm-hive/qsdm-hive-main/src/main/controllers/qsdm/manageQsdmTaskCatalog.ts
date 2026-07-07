import { Event } from 'electron';

import axios from 'axios';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import { submitQsdmTaskActionIntent } from 'main/services/qsdmTaskActions';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import {
  QsdmTaskAction,
  QsdmTaskCatalogManageRequest,
  QsdmTaskCatalogManageResponse,
  QsdmTaskResponse,
} from 'models/api/qsdm';

const getCurrentManifest = async (taskId: string) => {
  try {
    const response = await axios.get<QsdmTaskResponse>(
      buildQsdmCoreApiUrl(`/tasks/${encodeURIComponent(taskId)}`),
      { timeout: 10000 }
    );
    return response.data.task?.manifest;
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 404) {
      return undefined;
    }
    throw error;
  }
};

export const manageQsdmTaskCatalog = async (
  _: Event,
  request: QsdmTaskCatalogManageRequest
): Promise<QsdmTaskCatalogManageResponse> => {
  const sender = getQsdmTaskActionSender()?.trim().toLowerCase();
  if (!sender) {
    throw new Error(
      'The active QSDM signer is not ready. Import or unlock a QSDM keystore before publishing tasks.'
    );
  }

  const taskId = request.taskId.trim();
  const currentManifest = await getCurrentManifest(taskId);
  if (
    currentManifest?.manager &&
    currentManifest.manager.toLowerCase() !== sender
  ) {
    throw new Error(
      `Task ${taskId} is managed by a different QSDM wallet. Only ${currentManifest.manager} can change it.`
    );
  }

  let catalogAction: QsdmTaskAction;
  let payload: Record<string, unknown> | undefined;
  let catalogVersion: number | undefined;
  let created: boolean | undefined;

  if (request.operation === 'publish') {
    if (!request.draft) {
      throw new Error('A task manifest draft is required for publishing.');
    }
    created = !currentManifest;
    catalogVersion = currentManifest ? currentManifest.version + 1 : 1;
    catalogAction = currentManifest ? 'catalog-update' : 'catalog-register';
    payload = {
      ...request.draft,
      schema_version: 1,
      task_id: taskId,
      version: catalogVersion,
      manager: sender,
    };
  } else {
    if (!currentManifest) {
      throw new Error(`Task ${taskId} is not published in the QSDM catalog.`);
    }
    catalogAction =
      request.operation === 'pause' ? 'catalog-pause' : 'catalog-resume';
    catalogVersion = currentManifest.version;
  }

  const response = await submitQsdmTaskActionIntent({
    taskId,
    action: catalogAction,
    payload,
    waitForCommit: false,
  });

  return {
    ...response,
    operation: request.operation,
    catalogAction:
      catalogAction as QsdmTaskCatalogManageResponse['catalogAction'],
    catalogVersion,
    created,
  };
};
