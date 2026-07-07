import config from 'config';
import {
  QsdmTaskCatalogManageRequest,
  QsdmTaskCatalogManageResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  request: QsdmTaskCatalogManageRequest
): Promise<QsdmTaskCatalogManageResponse> =>
  sendMessage(config.endpoints.MANAGE_QSDM_TASK_CATALOG, request);
