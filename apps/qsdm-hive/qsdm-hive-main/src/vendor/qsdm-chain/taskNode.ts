import { PublicKey } from './web3';

export type TaskNodeConfig = Record<string, any>;
export type TaskData = Record<string, any>;
export type INode = Record<string, any>;
export type IRunningTasks<T> = Record<string, T>;

export interface IDatabase {
  get(key: string): Promise<any>;
  put?(key: string, value: any): Promise<any>;
}

export interface ITaskNodeBase {
  taskType?: string;
  storeGet?(key: string): Promise<any>;
  storeSet?(key: string, value: any): Promise<any>;
  [key: string]: any;
}

export class TaskNodeBase implements ITaskNodeBase {
  readonly config: TaskNodeConfig;

  taskType?: string;

  taskData: TaskData = {};

  mainSystemAccount?: any;

  private readonly storeAdapter?: IDatabase;

  private readonly memoryStore = new Map<string, any>();

  constructor(config: TaskNodeConfig = {}) {
    this.config = config;
    this.taskType = config.taskType;
    this.taskData = (config.taskData as TaskData) || {};
    this.mainSystemAccount = config.mainSystemAccount;
    this.storeAdapter = config.db as IDatabase | undefined;
  }

  async storeGet(key: string): Promise<any> {
    if (this.storeAdapter?.get) return this.storeAdapter.get(key);
    return this.memoryStore.get(key);
  }

  async storeSet(key: string, value: any): Promise<any> {
    if (this.storeAdapter?.put) return this.storeAdapter.put(key, value);
    this.memoryStore.set(key, value);
    return value;
  }

  async storeGetRaw(key: string): Promise<any> {
    return this.storeGet(key);
  }

  async getCurrentSlot(..._args: any[]) {
    return 0;
  }

  async getNodes(..._args: any[]) {
    return [];
  }

  async validateAndVoteOnNodes(..._args: any[]) {
    return null;
  }

  async uploadDistributionList(..._args: any[]) {
    return null;
  }

  async distributionListSubmissionOnChain(..._args: any[]) {
    return null;
  }

  async auditSubmission(..._args: any[]) {
    return null;
  }

  async claimReward(..._args: any[]): Promise<string> {
    return '';
  }

  setLoggerCallback(_callback?: (...args: any[]) => void) {
    return undefined;
  }
}

export const TASK_CONTRACT_ID = new PublicKey(
  'QsdmTask11111111111111111111111111111111111'
);
export const KPL_CONTRACT_ID = new PublicKey(
  'QsdmCell11111111111111111111111111111111111'
);
export const KPL_PROGRAM_ID = 'QsdmCellProgram1111111111111111111111111111';

export const TASK_INSTRUCTION_LAYOUTS = {
  Stake: 'Stake',
  Withdraw: 'Withdraw',
  Submit: 'Submit',
  Claim: 'Claim',
  ClaimReward: 'ClaimReward',
};

export const TASK_INSTRUCTION_LAYOUTS_KPL = TASK_INSTRUCTION_LAYOUTS;

export const encodeData = (_layout: unknown, value: unknown) =>
  Buffer.from(JSON.stringify(value ?? {}));

export const padStringWithSpaces = (value: string, length: number) =>
  value.padEnd(length, ' ').slice(0, length);

export enum LogLevel {
  Log = 'log',
  Info = 'info',
  Warn = 'warn',
  Error = 'error',
  Success = 'success',
}

export async function registerNodes(..._args: any[]) {
  return [];
}

export async function getCurrentSlot(..._args: any[]) {
  return 0;
}

export async function getAverageSlotTime(..._args: any[]) {
  return 420;
}

export async function getMyTaskStakeInfo(..._args: any[]): Promise<number> {
  return 0;
}

export async function getMyTaskSubmissionRoundInfo(
  ..._args: any[]
): Promise<any> {
  return null;
}

export async function getTaskState(..._args: any[]): Promise<Record<string, any>> {
  return {};
}

export async function getTaskStateKPL(
  ..._args: any[]
): Promise<Record<string, any>> {
  return {};
}

export async function getTaskSubmissionInfo(
  ..._args: any[]
): Promise<Record<string, any>> {
  return {};
}

export async function initialPropagation(..._args: any[]) {
  return null;
}

export async function runPeriodic(..._args: any[]) {
  return null;
}

export async function runTimers(..._args: any[]) {
  return null;
}

export async function updateRewardsQueue(..._args: any[]) {
  return null;
}
