import { loadAndExecuteTasks } from 'main/node';
import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

import getUserConfig from './getUserConfig';

export const initializeTasks = async (): Promise<void> => {
  try {
    const userConfig = await getUserConfig();
    if (userConfig?.hasFinishedTheMainnetMigration) {
      await qsdmHiveTasks.initializeTasks();
      await loadAndExecuteTasks();
    }
  } catch (err) {
    console.warn(
      'Task initialization failed; keeping QSDM Hive open with cached/offline data.',
      err
    );
  }
};
