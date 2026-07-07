import { getRunnedPrivateTasks } from '../controllers/privateTasks';
import startTask from '../controllers/startTask';
import qsdmHiveTasks from '../services/qsdmHiveTasks';

const executeTasks = async (): Promise<void> => {
  const executableTaskPubkeys = await qsdmHiveTasks.getRunningTaskPubKeys();
  console.log('STARTING TASKS: ', executableTaskPubkeys);

  const promises = executableTaskPubkeys.map(async (publicKey) => {
    const privateTasks = await getRunnedPrivateTasks();
    const isPrivate = privateTasks.includes(publicKey);

    return startTask({} as Event, { taskAccountPubKey: publicKey, isPrivate });
  });

  await Promise.all(promises);
};

export default executeTasks;
