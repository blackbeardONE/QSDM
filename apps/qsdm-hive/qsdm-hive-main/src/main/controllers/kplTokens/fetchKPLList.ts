import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { fetchKPLListResponse } from 'models';

export const fetchKPLList = async (): Promise<fetchKPLListResponse> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    return {
      name: 'QSDM CELL tokens',
      tokenList: [],
    };
  }

  return {
    name: 'QSDM CELL tokens',
    tokenList: [],
  };
};
