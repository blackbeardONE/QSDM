import {
  GetAvailableTasksParam,
  GetMyTasksParam,
  PaginatedResponse,
} from 'models';
import { Task } from 'renderer/types';
import { TaskMigrationWalk } from 'utils/taskMigrationWalk';

import { parseTask } from './utils';

/** K2 Queries */
export const getTasksById = async (tasksIds: string[]) => {
  const tasks = await window.main.getTasksById({ tasksIds });
  return tasks.filter(Boolean).map(parseTask);
};

export const getLatestTaskById = async (
  taskId: string,
  walk: TaskMigrationWalk = new TaskMigrationWalk()
): Promise<Task | null> => {
  // A pointer back to a task already seen (a cycle) or to another manager's
  // task ends the walk: see utils/taskMigrationWalk.
  if (!walk.enter(taskId)) return null;

  const task = await window.main.getTaskInfo({ taskAccountPubKey: taskId });

  if (!task) return null;
  if (!walk.sameManager(task.taskManager)) return null;

  if (task.isMigrated) {
    return getLatestTaskById(task.migratedTo, walk);
  }

  return { ...task, publicKey: taskId };
};

export const getLatestTasksById = async (tasksIds: string[]) => {
  // Each id starts its own walk (map would pass the index as the walk).
  const tasks = await Promise.all(
    tasksIds.map((taskId) => getLatestTaskById(taskId))
  );
  return tasks.filter((task): task is Task => task !== null);
};

export const fetchAvailableTasks = async (
  params: GetAvailableTasksParam
): Promise<PaginatedResponse<Task>> => {
  const response = await window.main.getAvailableTasks(params);
  return {
    ...response,
    content: response.content.map(parseTask),
  };
};

export const fetchPrivateTasks = async (
  params: GetAvailableTasksParam
): Promise<PaginatedResponse<Task>> => {
  const response = await window.main.getPrivateTasks(params);
  return {
    ...response,
    content: response.content.map(parseTask),
  };
};

export const getAccountBalance = async (pubKey: string) => {
  const balance = await window.main.getAccountBalance(pubKey);
  return balance;
};

export const getAverageSlotTime = async (): Promise<number> => {
  const averageSlotTime = await window.main.getAverageSlotTime();
  return averageSlotTime;
};

export const getAllAccounts = async () => {
  const accounts = await window.main.getAllAccounts();
  return accounts;
};

export const getLastSubmissionTime = async (
  taskPublicKey: string,
  stakingPublicKey: string,
  averageSlotTime: number
): Promise<number> => {
  return window.main.getLastSubmissionTime({
    taskPublicKey,
    stakingPublicKey,
    averageSlotTime,
  });
};

export const getCurrentSlot = async () => {
  return window.main.getCurrentSlot();
};

export const initializeTasks = async () => {
  return window.main.initializeTasks();
};

export const getTimeToNextReward = async (averageSlotTime: number) => {
  return window.main.getTimeToNextReward(averageSlotTime);
};

export const getTaskInfo = async (taskPublicKey: string) => {
  return window.main.getTaskInfo({ taskAccountPubKey: taskPublicKey });
};

/** indirect K2 queries or using K2 data */
export const fetchMyTasks = async (
  params: GetMyTasksParam
): Promise<PaginatedResponse<Task>> => {
  const response = await window.main.getMyTasks(params);
  return {
    ...response,
    content: response.content.map(parseTask),
  };
};

export const getTaskNodeInfo = async () => {
  const res = await window.main.getTaskNodeInfo();
  return res;
};
