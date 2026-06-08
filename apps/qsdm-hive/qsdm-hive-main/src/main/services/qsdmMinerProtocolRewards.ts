import axios from 'axios';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import { QSDM_MINER_SYSTEM_TASK_ID } from 'config/qsdmSystemTasks';
import { SystemDbKeys } from 'config/systemDbKeys';
import { namespaceInstance } from 'main/node/helpers/Namespace';
import { QsdmMiningAccountResponse } from 'models/api/qsdm';

import { getQsdmMinerRewardAddress } from './qsdmSystemTasks';
import { qsdmCellToDenomination, readFiniteNumber } from './qsdmTaskStake';

type MinerProtocolRewardBaseline = {
  address: string;
  baselineCell: number;
  updatedAt: string;
};

type MinerProtocolRewardBaselines = Record<string, MinerProtocolRewardBaseline>;

export type QsdmMinerProtocolRewardInfo = {
  address: string;
  balanceCell: number;
  baselineCell: number;
  earnedCell: number;
  earnedDenomination: number;
};

const buildMiningAccountUrl = (address: string) => {
  const url = new URL(buildQsdmCoreApiUrl('/mining/account'));
  url.searchParams.set('address', address);
  return url.toString();
};

const parseBaselines = (raw: unknown): MinerProtocolRewardBaselines => {
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {};
    }
    return parsed as MinerProtocolRewardBaselines;
  } catch {
    return {};
  }
};

const getStoredBaselines = async () =>
  parseBaselines(
    await namespaceInstance.storeGet(
      SystemDbKeys.QsdmMinerProtocolRewardBaselines
    )
  );

const storeBaselines = async (baselines: MinerProtocolRewardBaselines) => {
  await namespaceInstance.storeSet(
    SystemDbKeys.QsdmMinerProtocolRewardBaselines,
    JSON.stringify(baselines)
  );
};

export const getQsdmMinerProtocolBalanceCell = async (address: string) => {
  const response = await axios.get<QsdmMiningAccountResponse>(
    buildMiningAccountUrl(address),
    { timeout: 10000 }
  );
  return readFiniteNumber(response.data.balance) || 0;
};

export const getQsdmMinerProtocolRewardInfo =
  async (): Promise<QsdmMinerProtocolRewardInfo | null> => {
    const address = getQsdmMinerRewardAddress();
    if (!address) {
      return null;
    }

    const balanceCell = await getQsdmMinerProtocolBalanceCell(address);
    const baselines = await getStoredBaselines();
    const baselineKey = `${QSDM_MINER_SYSTEM_TASK_ID}:${address}`;
    const storedBaseline = readFiniteNumber(baselines[baselineKey]?.baselineCell);

    let baselineCell = storedBaseline;
    if (baselineCell === undefined || baselineCell > balanceCell) {
      baselineCell = balanceCell;
      baselines[baselineKey] = {
        address,
        baselineCell,
        updatedAt: new Date().toISOString(),
      };
      await storeBaselines(baselines);
    }

    const earnedCell = Math.max(0, balanceCell - baselineCell);

    return {
      address,
      balanceCell,
      baselineCell,
      earnedCell,
      earnedDenomination: qsdmCellToDenomination(earnedCell),
    };
  };
