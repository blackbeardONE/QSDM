import { TaskType } from 'models/task';

type QsdmTaskTypeInput = {
  taskType?: unknown;
  tokenType?: unknown;
};

const hasTokenMint = (tokenType: unknown) =>
  typeof tokenType === 'string'
    ? tokenType.trim().length > 0
    : Boolean(tokenType);

// QSDM's early task catalog used KOII for native CELL tasks. A token mint is
// the authoritative KPL signal; every task without one is a CELL task.
export const normalizeQsdmTaskType = ({
  taskType,
  tokenType,
}: QsdmTaskTypeInput): TaskType =>
  String(taskType || '')
    .trim()
    .toUpperCase() === 'KPL' || hasTokenMint(tokenType)
    ? 'KPL'
    : 'CELL';
