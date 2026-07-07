import { namespaceInstance } from 'main/node/helpers/Namespace';
import { TaskVariablesReturnType } from 'models/api/taskVariables/getStoredTaskVariables';

import { PersistentStoreKeys } from '../types';

export const getStoredTaskVariables =
  async (): Promise<TaskVariablesReturnType> => {
    const taskVariablesStringified: string = await namespaceInstance.storeGet(
      PersistentStoreKeys.TaskVariables
    );

    if (!taskVariablesStringified || taskVariablesStringified === 'undefined') {
      return {};
    }

    try {
      const taskVariables: TaskVariablesReturnType = {
        ...(JSON.parse(taskVariablesStringified) as TaskVariablesReturnType),
      };

      return taskVariables;
    } catch (error) {
      console.log('Get Stored Task Variables: JSON parse error', error);
      return {};
    }
  };
