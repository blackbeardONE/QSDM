import axios from 'axios';

const mockGetQsdmTaskActionSender = jest.fn();

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () => mockGetQsdmTaskActionSender(),
}));

import {
  createQsdmEdgeWorkerSystemTask,
  createQsdmMinerSystemTask,
  getQsdmSystemTaskById,
  getQsdmSystemTaskMetadata,
  mergeQsdmSystemTasks,
  QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT,
  QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
  QSDM_MINER_MIN_STAKE_AMOUNT,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT,
  QSDM_SKYFANG_LINK_REWARD_CELL,
  QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID,
  createQsdmSkyFangLinkSystemTask,
  verifyQsdmSkyFangWalletLinked,
} from './qsdmSystemTasks';

const mockedAxiosGet = axios.get as jest.Mock;
const linkedSender =
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';

describe('qsdmSystemTasks', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
    mockGetQsdmTaskActionSender.mockReset();
    mockGetQsdmTaskActionSender.mockReturnValue(linkedSender);
  });

  it('creates a permanent QSDM miner task shape accepted by Hive task lists', () => {
    const task = createQsdmMinerSystemTask();

    expect(task.task_id).toBe(QSDM_MINER_SYSTEM_TASK_ID);
    expect(task.task_name).toBe('QSDM Miner');
    expect(task.task_metadata).toBe(QSDM_MINER_SYSTEM_TASK_METADATA_ID);
    expect(task.is_allowlisted).toBe(true);
    expect(task.is_active).toBe(true);
    expect(task.minimum_stake_amount).toBe(QSDM_MINER_MIN_STAKE_AMOUNT);
    expect(task.task_type).toBe('CELL');
    expect(JSON.parse(task.task_vars)).toEqual({
      qsdm_system_task: true,
      no_expiry: true,
      reward_source: 'protocol-mining-emission',
      hive_task_bounty: false,
      requires_skyfang_wallet_link: true,
      required_task_id: QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
    });
    expect(task.task_description).toContain(
      'there is no separate Hive bounty'
    );
    expect(task.task_description).toContain('Sky Fang account linked');
  });

  it('creates a CPU-only QSDM edge worker task shape accepted by Hive task lists', () => {
    const task = createQsdmEdgeWorkerSystemTask();

    expect(task.task_id).toBe(QSDM_EDGE_WORKER_SYSTEM_TASK_ID);
    expect(task.task_name).toBe('QSDM Edge Worker');
    expect(task.task_metadata).toBe(QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID);
    expect(task.is_allowlisted).toBe(true);
    expect(task.is_active).toBe(true);
    expect(task.minimum_stake_amount).toBe(
      QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT
    );
    expect(task.total_bounty_amount).toBe(
      QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL
    );
    expect(task.bounty_amount_per_round).toBe(
      QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL
    );
    expect(task.task_type).toBe('CELL');
    expect(JSON.parse(task.task_vars)).toEqual({
      qsdm_system_task: true,
      no_expiry: true,
      cpu_worker: true,
      reward_source: 'funded-pool',
      reward_per_round_cell: QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
      reward_pool_target_cell: QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
    });
  });

  it('prepends the built-in miner without duplicating a core-provided copy', () => {
    const coreTask = createQsdmMinerSystemTask({
      task_name: 'Core Provided Miner',
    });
    const coreEdgeTask = createQsdmEdgeWorkerSystemTask({
      task_name: 'Core Provided Edge Worker',
    });
    const coreSkyFangLinkTask = createQsdmSkyFangLinkSystemTask({
      task_name: 'Core Provided Sky Fang Link',
    });
    const otherTask = createQsdmMinerSystemTask({
      task_id: 'qsdm-example-task',
      task_name: 'Example Task',
    });

    const merged = mergeQsdmSystemTasks([
      otherTask,
      coreTask,
      coreEdgeTask,
      coreSkyFangLinkTask,
    ]);

    expect(merged.map((task) => task.task_id)).toEqual([
      QSDM_MINER_SYSTEM_TASK_ID,
      QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      'qsdm-example-task',
    ]);
    expect(merged[0].task_name).toBe('Core Provided Miner');
    expect(merged[1].task_name).toBe('Core Provided Edge Worker');
    expect(merged[2].task_name).toBe('Core Provided Sky Fang Link');
  });

  it('creates the one-time Sky Fang wallet-link task shape accepted by Hive task lists', () => {
    const task = createQsdmSkyFangLinkSystemTask();

    expect(task.task_id).toBe(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID);
    expect(task.task_name).toBe('QSDM Sky Fang Link');
    expect(task.task_metadata).toBe(QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID);
    expect(task.is_allowlisted).toBe(true);
    expect(task.is_active).toBe(true);
    expect(task.minimum_stake_amount).toBe(
      QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT
    );
    expect(task.total_bounty_amount).toBe(
      QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL
    );
    expect(task.bounty_amount_per_round).toBe(QSDM_SKYFANG_LINK_REWARD_CELL);
    expect(task.task_type).toBe('CELL');
    expect(JSON.parse(task.task_vars)).toEqual({
      qsdm_system_task: true,
      no_expiry: true,
      skyfang_wallet_link: true,
      one_time_reward: true,
      reward_source: 'funded-pool',
      reward_per_link_cell: QSDM_SKYFANG_LINK_REWARD_CELL,
      reward_pool_target_cell: QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
      skyfang_base_url: 'https://skyfang.xyz',
    });
  });

  it('resolves miner task data and metadata by either task or metadata id', () => {
    expect(getQsdmSystemTaskById(QSDM_MINER_SYSTEM_TASK_ID)?.task_name).toBe(
      'QSDM Miner'
    );
    expect(
      getQsdmSystemTaskMetadata(QSDM_MINER_SYSTEM_TASK_METADATA_ID)?.tags
    ).toEqual([
      'QSDM',
      'CELL',
      'Miner',
      'NVIDIA GPU Required',
      'Protocol Emission',
      'No Hive Bounty',
      'Sky Fang Link Required',
      'CC 7.5+',
      'No Expiry',
    ]);
    expect(
      getQsdmSystemTaskMetadata(QSDM_MINER_SYSTEM_TASK_METADATA_ID)
        ?.requirementsTags
    ).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          type: 'ADDON',
          value: 'Protocol mining emission',
        }),
      ])
    );
    expect(
      getQsdmSystemTaskById(QSDM_EDGE_WORKER_SYSTEM_TASK_ID)?.task_name
    ).toBe('QSDM Edge Worker');
    expect(
      getQsdmSystemTaskMetadata(QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID)?.tags
    ).toEqual([
      'QSDM',
      'CELL',
      'CPU',
      'Edge Worker',
      'No NVIDIA GPU',
      'No Expiry',
    ]);
    expect(
      getQsdmSystemTaskById(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID)?.task_name
    ).toBe('QSDM Sky Fang Link');
    expect(
      getQsdmSystemTaskMetadata(QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID)?.tags
    ).toEqual([
      'QSDM',
      'CELL',
      'Sky Fang',
      'Wallet Link',
      'Mandatory',
      'One-Time Reward',
      'No GPU',
    ]);
  });

  it('verifies the active QSDM wallet is linked to Sky Fang', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        ok: true,
        linked: true,
        address: linkedSender,
        linked_at: '2026-06-06T00:00:00Z',
        site: 'https://skyfang.xyz',
      },
    });

    await expect(verifyQsdmSkyFangWalletLinked()).resolves.toEqual({
      ok: true,
      sender: linkedSender,
      linkedAt: '2026-06-06T00:00:00Z',
      site: 'https://skyfang.xyz',
    });
    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'https://skyfang.xyz/api/qsdm/link-status',
      {
        params: { address: linkedSender },
        timeout: 15000,
      }
    );
  });

  it('fails the Sky Fang wallet gate when the linked wallet is different', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        ok: true,
        linked: true,
        address:
          'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
      },
    });

    const result = await verifyQsdmSkyFangWalletLinked();

    expect(result.ok).toBe(false);
    expect(result.detail).toContain('Sky Fang is linked to');
    expect(result.detail).toContain('active Hive wallet');
  });

  it('fails the Sky Fang wallet gate when Sky Fang says the active wallet is not linked', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        ok: true,
        linked: false,
        address: linkedSender,
        site: 'https://skyfang.xyz',
      },
    });

    const result = await verifyQsdmSkyFangWalletLinked();

    expect(result.ok).toBe(false);
    expect(result.sender).toBe(linkedSender);
    expect(result.detail).toContain(
      'Sky Fang account is not linked to the active QSDM wallet yet'
    );
  });
});
