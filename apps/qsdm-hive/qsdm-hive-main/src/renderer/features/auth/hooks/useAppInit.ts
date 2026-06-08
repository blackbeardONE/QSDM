import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from 'react-query';

import { useUserAppConfig } from 'renderer/features/settings/hooks';
import { QueryKeys, initializeTasks, startAllTasks } from 'renderer/services';

import { usePrefetchAppData } from './usePrefetchAppData';

const NODE_INITIALIZATION_TIMEOUT = 8000;

export function useAppInit() {
  const queryClient = useQueryClient();

  usePrefetchAppData();

  const { userConfig: settings, isUserConfigLoading: loadingSettings } =
    useUserAppConfig();

  const initializeNodeCalled = useRef(false);

  const [initializingNode, setInitializingNode] = useState(true);

  useEffect(() => {
    const initializeNode = async () => {
      if (initializeNodeCalled.current) {
        return;
      }
      initializeNodeCalled.current = true;
      console.log('Initializing node...');
      try {
        await initializeTasks();
        await startAllTasks();
        queryClient.invalidateQueries([QueryKeys.TaskList]);
      } catch (error) {
        console.error('Node initialization failed; opening app anyway.', error);
      } finally {
        setTimeout(() => {
          setInitializingNode(false);
        }, NODE_INITIALIZATION_TIMEOUT);
      }
    };

    const shouldInitializeNode =
      !loadingSettings && settings?.hasFinishedTheMainnetMigration;

    if (shouldInitializeNode) {
      initializeNode();
    } else {
      setInitializingNode(false);
    }
  }, [loadingSettings, settings?.hasFinishedTheMainnetMigration, queryClient]);

  return {
    initializingNode,
  };
}
