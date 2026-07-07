import axios from 'axios';
import fs from 'fs';
import os from 'os';
import path from 'path';

const mockGetQsdmTaskActionSender = jest.fn();

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () => mockGetQsdmTaskActionSender(),
}));

import {
  createQsdmEdgeWorkerSystemTask,
  createQsdmEdgeWorkerScript,
  createQsdmGPUWorkerSystemTask,
  createQsdmMinerSystemTask,
  createQsdmMotherHiveSystemTask,
  createQsdmRAMWorkerSystemTask,
  buildQsdmMinerLaunchArgs,
  isMinerProcessFromCandidates,
  isPortableHiveMinerProcess,
  buildProcessStartupExitDetail,
  getQsdmSystemTaskById,
  getQsdmSystemTaskMetadata,
  getDefaultEdgeRelayTokenFile,
  getDefaultEdgeRelayURL,
  getMinerExecutableCandidates,
  mergeQsdmSystemTasks,
  normalizeQsdmSystemTaskCoreCellAmount,
  resolveQsdmMinerValidatorBaseUrl,
  QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT,
  QSDM_EDGE_WORKER_GPU_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_GPU_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID,
  QSDM_EDGE_WORKER_RAM_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_RAM_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID,
  QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
  QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
  QSDM_EDGE_WORKER_SYSTEM_TASK_ID,
  QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID,
  QSDM_MINER_MIN_STAKE_AMOUNT,
  QSDM_MINER_SYSTEM_TASK_ID,
  QSDM_MINER_SYSTEM_TASK_METADATA_ID,
  QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT,
  QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
  QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID,
  QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT,
  QSDM_SKYFANG_LINK_BASE_REWARD_CELL,
  QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
  QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE,
  QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL,
  QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
  QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID,
  createQsdmSkyFangLinkSystemTask,
  getRewardedQsdmTaskSubmissionForSender,
  getRewardedQsdmTaskSubmissionForSenderRound,
  hasRewardedQsdmTaskSubmissionForSender,
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

  it('prefers the packaged console miner on Linux and Windows', () => {
    const linuxCandidates = getMinerExecutableCandidates({
      platform: 'linux',
      resourcesPath: '/opt/qsdm-hive/resources',
      executablePath: '/opt/qsdm-hive/qsdm-hive',
      workingDirectory: '/tmp/qsdm-hive',
      env: {},
    });
    const windowsCandidates = getMinerExecutableCandidates({
      platform: 'win32',
      resourcesPath: 'C:\\Program Files\\QSDM Hive\\resources',
      executablePath: 'C:\\Program Files\\QSDM Hive\\QSDM Hive.exe',
      workingDirectory: 'C:\\src\\qsdm-hive',
      env: { ProgramFiles: 'C:\\Program Files' },
    });

    expect(linuxCandidates[0]).toBe(
      '/opt/qsdm-hive/resources/miner/qsdmminer-console'
    );
    expect(windowsCandidates[0]).toBe(
      'C:\\Program Files\\QSDM Hive\\resources\\miner\\qsdmminer-console.exe'
    );
    expect(linuxCandidates).not.toContain(
      '/opt/qsdm-hive/resources/miner/qsdmminer.exe'
    );
  });

  it('does not adopt a protected legacy miner service as the Hive CUDA miner', () => {
    const packaged =
      'C:\\Program Files\\QSDM Hive\\resources\\miner\\qsdmminer-console.exe';

    expect(
      isMinerProcessFromCandidates(
        {
          pid: 123,
          executablePath: undefined,
          commandLine: undefined,
        },
        [packaged]
      )
    ).toBe(false);
    expect(
      isMinerProcessFromCandidates(
        {
          pid: 456,
          executablePath: packaged,
          commandLine: `${packaged} --compute-backend=cuda`,
        },
        [packaged]
      )
    ).toBe(true);
  });

  it('adopts a Hive CUDA miner left under an older Linux AppImage mount', () => {
    expect(
      isPortableHiveMinerProcess(
        {
          pid: 789,
          executablePath: 'qsdmminer-conso',
          commandLine:
            '/tmp/.mount_qsdm-old/resources/miner/qsdmminer-console --config=/home/test/.qsdm/miner.toml --log-file=/home/test/.qsdm/miner.log --compute-backend=cuda',
        },
        '/home/test/.qsdm/miner.toml'
      )
    ).toBe(true);
  });

  it('does not adopt an unrelated console miner without Hive CUDA arguments', () => {
    expect(
      isPortableHiveMinerProcess(
        {
          pid: 790,
          executablePath: '/usr/local/bin/qsdmminer-console',
          commandLine:
            '/usr/local/bin/qsdmminer-console --config=/tmp/other.toml --compute-backend=cpu',
        },
        '/home/test/.qsdm/miner.toml'
      )
    ).toBe(false);
  });

  it('requires the CUDA solver without forcing GPU-idle gating', () => {
    expect(
      buildQsdmMinerLaunchArgs({
        configPath: '/home/test/.qsdm/miner.toml',
        logPath: '/home/test/.qsdm/miner.log',
        env: {},
      })
    ).toEqual([
      '--config=/home/test/.qsdm/miner.toml',
      '--log-file=/home/test/.qsdm/miner.log',
      '--log-size-mb=10',
      '--log-keep=5',
      '--plain',
      '--compute-backend=cuda',
    ]);
  });

  it('keeps legacy GPU-idle gating as an explicit operator opt-in', () => {
    expect(
      buildQsdmMinerLaunchArgs({
        configPath: 'miner.toml',
        logPath: 'miner.log',
        env: { QSDM_MINER_IDLE_ONLY: 'true' },
      })
    ).toEqual([
      '--config=miner.toml',
      '--idle-only',
      '--idle-threshold=10',
      '--idle-grace=60s',
      '--log-file=miner.log',
      '--log-size-mb=10',
      '--log-keep=5',
      '--plain',
      '--compute-backend=cuda',
    ]);
  });

  it('routes official local and gateway miners to canonical Core', () => {
    const canonicalApiUrl = 'https://canonical.example/api/v1';

    expect(
      resolveQsdmMinerValidatorBaseUrl({
        runtimeApiUrl: 'http://127.0.0.1:8080/api/v1',
        canonicalApiUrl,
        configuredUrl: '',
      })
    ).toBe('https://canonical.example');
    expect(
      resolveQsdmMinerValidatorBaseUrl({
        runtimeApiUrl: 'https://api.qsdm.tech/attest/home-validator/api/v1',
        canonicalApiUrl,
        configuredUrl: '',
      })
    ).toBe('https://canonical.example');
  });

  it('preserves explicit custom miner networks and operator overrides', () => {
    expect(
      resolveQsdmMinerValidatorBaseUrl({
        runtimeApiUrl: 'https://devnet.example/api/v1',
        canonicalApiUrl: 'https://canonical.example/api/v1',
        configuredUrl: '',
      })
    ).toBe('https://devnet.example');
    expect(
      resolveQsdmMinerValidatorBaseUrl({
        runtimeApiUrl: 'http://127.0.0.1:8080/api/v1',
        canonicalApiUrl: 'https://canonical.example/api/v1',
        configuredUrl: 'https://operator.example/api/v1/',
      })
    ).toBe('https://operator.example');
  });

  it('explains NVIDIA v2 startup refusal and includes a safe miner log tail', () => {
    const directory = fs.mkdtempSync(
      path.join(os.tmpdir(), 'qsdm-hive-miner-')
    );
    const logPath = path.join(directory, 'miner.log');
    fs.writeFileSync(
      logPath,
      'preflight: validator accepts protocol v2 only\nhmac_key=do-not-display\n',
      'utf8'
    );

    try {
      const detail = buildProcessStartupExitDetail(
        'QSDM Miner',
        3,
        null,
        logPath
      );

      expect(detail).toContain('refused legacy mining protocol v1');
      expect(detail).toContain(
        'separately confirmed on-chain miner enrollment bond'
      );
      expect(detail).toContain('validator accepts protocol v2 only');
      expect(detail).toContain('hmac_key=[redacted]');
      expect(detail).not.toContain('do-not-display');
    } finally {
      fs.rmSync(directory, { recursive: true, force: true });
    }
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
    });
    expect(task.task_description).toContain('there is no separate Hive bounty');
    expect(task.task_description).toContain(
      'A Sky Fang account is not required for mining'
    );
  });

  it('creates a CPU-only QSDM edge worker task shape accepted by Hive task lists', () => {
    const task = createQsdmEdgeWorkerSystemTask();

    expect(task.task_id).toBe(QSDM_EDGE_WORKER_SYSTEM_TASK_ID);
    expect(task.task_name).toBe('QSDM Edge Worker CPU');
    expect(task.task_metadata).toBe(QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID);
    expect(task.is_allowlisted).toBe(true);
    expect(task.is_active).toBe(true);
    expect(task.minimum_stake_amount).toBe(QSDM_EDGE_WORKER_MIN_STAKE_AMOUNT);
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
      resource_worker: 'cpu',
      cpu_worker: true,
      pooled_compute: true,
      coordinator_receipts: true,
      relay_receipts: true,
      mother_hive: true,
      reward_source: 'funded-pool',
      reward_per_round_cell: QSDM_EDGE_WORKER_REWARD_PER_ROUND_CELL,
      reward_pool_target_cell: QSDM_EDGE_WORKER_REWARD_POOL_TARGET_CELL,
    });
  });

  it('creates separate GPU and RAM pooled-compute tasks', () => {
    const gpu = createQsdmGPUWorkerSystemTask();
    const ram = createQsdmRAMWorkerSystemTask();

    expect(gpu).toEqual(
      expect.objectContaining({
        task_id: QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
        task_name: 'QSDM Edge Worker GPU',
        task_metadata: QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID,
        bounty_amount_per_round: QSDM_EDGE_WORKER_GPU_REWARD_PER_ROUND_CELL,
        total_bounty_amount: QSDM_EDGE_WORKER_GPU_REWARD_POOL_TARGET_CELL,
        task_type: 'CELL',
      })
    );
    expect(JSON.parse(gpu.task_vars)).toEqual(
      expect.objectContaining({
        resource_worker: 'gpu',
        cpu_worker: false,
        pooled_compute: true,
        coordinator_receipts: true,
        relay_receipts: true,
        mother_hive: true,
      })
    );
    expect(ram).toEqual(
      expect.objectContaining({
        task_id: QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
        task_name: 'QSDM Edge Worker RAM',
        task_metadata: QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID,
        bounty_amount_per_round: QSDM_EDGE_WORKER_RAM_REWARD_PER_ROUND_CELL,
        total_bounty_amount: QSDM_EDGE_WORKER_RAM_REWARD_POOL_TARGET_CELL,
        task_type: 'CELL',
      })
    );
    expect(JSON.parse(ram.task_vars)).toEqual(
      expect.objectContaining({
        resource_worker: 'ram',
        cpu_worker: false,
        pooled_compute: true,
        coordinator_receipts: true,
        relay_receipts: true,
        mother_hive: true,
      })
    );
  });

  it('creates Mother Hive with chain-enforced 70/15/15 settlement', () => {
    const task = createQsdmMotherHiveSystemTask();

    expect(QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT).toBe(70);
    expect(QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT).toBe(15);
    expect(QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT).toBe(15);
    expect(
      QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT +
        QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT +
        QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT
    ).toBe(100);

    expect(task).toEqual(
      expect.objectContaining({
        task_id: QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
        task_name: 'Mother Hive Task',
        task_metadata: QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID,
        minimum_stake_amount: QSDM_MOTHER_HIVE_MIN_STAKE_AMOUNT,
        bounty_amount_per_round: 0,
        task_type: 'CELL',
      })
    );
    expect(JSON.parse(task.task_vars)).toEqual(
      expect.objectContaining({
        mother_hive_role: true,
        qsdm_hive_only: true,
        workload_mode: 'qsdm-approved-distributed-jobs',
        contributor_share_percent: QSDM_MOTHER_HIVE_CONTRIBUTOR_SHARE_PERCENT,
        mother_hive_share_percent: QSDM_MOTHER_HIVE_OPERATOR_SHARE_PERCENT,
        ecosystem_share_percent: QSDM_MOTHER_HIVE_ECOSYSTEM_SHARE_PERCENT,
        settlement_active: true,
        settlement_protocol: 'qsdm-edge-settlement/v1',
      })
    );
    expect(task.task_description).toContain('globally replay-protected');
  });

  it('fails closed when Mother Hive has a configured relay', () => {
    const script = createQsdmEdgeWorkerScript();

    expect(script).toContain('const relayRequired = Boolean(relayTokenFile);');
    expect(script).toContain('source: settlementSource');
    expect(script).toContain("'/v1/settlement/bind'");
    expect(script).toContain("'QSDM-EDGE-RELAY-ID\\0'");
    expect(script).toContain('if (relayRequired) throw error;');
    expect(script).toContain(
      'QSDM Mother Hive waiting for a verified relay receipt'
    );
    expect(script).not.toContain('; using local bounded work');
  });

  it('loads the one-click Edge Control Mother Hive connection', () => {
    const originalAppData = process.env.APPDATA;
    const originalRelayURL = process.env.QSDM_EDGE_RELAY_URL;
    const originalRelayToken = process.env.QSDM_EDGE_RELAY_TOKEN_FILE;
    delete process.env.QSDM_EDGE_RELAY_URL;
    delete process.env.QSDM_EDGE_RELAY_TOKEN_FILE;
    process.env.APPDATA = 'C:\\Users\\tester\\AppData\\Roaming';
    const readFile = jest.spyOn(fs, 'readFileSync').mockImplementation(((
      filePath: fs.PathOrFileDescriptor
    ) => {
      if (String(filePath).endsWith('mother-hive.json')) {
        return JSON.stringify({
          schema_version: 1,
          relay_url: 'http://192.168.1.10:7740',
          token_file:
            'C:\\Users\\tester\\AppData\\Roaming\\QSDM\\edge-pool\\mother-hive.token',
        });
      }
      throw new Error(`Unexpected read: ${String(filePath)}`);
    }) as typeof fs.readFileSync);
    const exists = jest
      .spyOn(fs, 'existsSync')
      .mockImplementation((filePath) =>
        String(filePath).endsWith('mother-hive.token')
      );

    expect(getDefaultEdgeRelayURL()).toBe('http://192.168.1.10:7740');
    expect(getDefaultEdgeRelayTokenFile()).toBe(
      'C:\\Users\\tester\\AppData\\Roaming\\QSDM\\edge-pool\\mother-hive.token'
    );

    readFile.mockRestore();
    exists.mockRestore();
    if (originalAppData === undefined) delete process.env.APPDATA;
    else process.env.APPDATA = originalAppData;
    if (originalRelayURL === undefined) delete process.env.QSDM_EDGE_RELAY_URL;
    else process.env.QSDM_EDGE_RELAY_URL = originalRelayURL;
    if (originalRelayToken === undefined)
      delete process.env.QSDM_EDGE_RELAY_TOKEN_FILE;
    else process.env.QSDM_EDGE_RELAY_TOKEN_FILE = originalRelayToken;
  });

  it('prepends the built-in miner without duplicating a core-provided copy', () => {
    const coreTask = createQsdmMinerSystemTask({
      task_name: 'Core Provided Miner',
      minimum_stake_amount: 1_000_000_000,
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
      QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID,
      QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID,
      QSDM_MOTHER_HIVE_SYSTEM_TASK_ID,
      QSDM_SKYFANG_LINK_SYSTEM_TASK_ID,
      'qsdm-example-task',
    ]);
    expect(merged[0].task_name).toBe('Core Provided Miner');
    expect(merged[0].minimum_stake_amount).toBe(0);
    expect(merged[1].task_name).toBe('Core Provided Edge Worker');
    expect(merged[2].task_name).toBe('QSDM Edge Worker GPU');
    expect(merged[3].task_name).toBe('QSDM Edge Worker RAM');
    expect(merged[4].task_name).toBe('Mother Hive Task');
    expect(merged[5].task_name).toBe('Core Provided Sky Fang Link');
  });

  it('does not allow legacy catalog types to overwrite system tasks as KOII', () => {
    const legacyOverride = {
      task_type: 'KOII',
    } as unknown as Parameters<typeof createQsdmMinerSystemTask>[0];

    expect(createQsdmMinerSystemTask(legacyOverride).task_type).toBe('CELL');
    expect(createQsdmEdgeWorkerSystemTask(legacyOverride).task_type).toBe(
      'CELL'
    );
    expect(createQsdmGPUWorkerSystemTask(legacyOverride).task_type).toBe(
      'CELL'
    );
    expect(createQsdmRAMWorkerSystemTask(legacyOverride).task_type).toBe(
      'CELL'
    );
    expect(createQsdmMotherHiveSystemTask(legacyOverride).task_type).toBe(
      'CELL'
    );
    expect(createQsdmSkyFangLinkSystemTask(legacyOverride).task_type).toBe(
      'CELL'
    );
  });

  it('creates the ongoing stake-weighted Sky Fang wallet-link task shape accepted by Hive task lists', () => {
    const task = createQsdmSkyFangLinkSystemTask();

    expect(task.task_id).toBe(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID);
    expect(task.task_name).toBe('QSDM Sky Fang Link');
    expect(task.task_metadata).toBe(QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID);
    expect(task.is_allowlisted).toBe(true);
    expect(task.is_active).toBe(true);
    expect(task.minimum_stake_amount).toBe(QSDM_SKYFANG_LINK_MIN_STAKE_AMOUNT);
    expect(task.total_bounty_amount).toBe(
      QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL
    );
    expect(task.bounty_amount_per_round).toBe(
      QSDM_SKYFANG_LINK_BASE_REWARD_CELL
    );
    expect(task.task_type).toBe('CELL');
    expect(JSON.parse(task.task_vars)).toEqual({
      qsdm_system_task: true,
      no_expiry: true,
      skyfang_wallet_link: true,
      reward_model: 'skyfang-hive-combined-stake-v1',
      reward_source: 'funded-pool',
      base_reward_cell: QSDM_SKYFANG_LINK_BASE_REWARD_CELL,
      hive_stake_reward_rate: QSDM_SKYFANG_LINK_HIVE_STAKE_REWARD_RATE,
      skyfang_stake_reward_rate: QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
      max_reward_per_round_cell: QSDM_SKYFANG_LINK_MAX_REWARD_PER_ROUND_CELL,
      reward_pool_target_cell: QSDM_SKYFANG_LINK_REWARD_POOL_TARGET_CELL,
      skyfang_base_url: 'https://skyfang.xyz',
    });
  });

  it('resolves miner task data and metadata by either task or metadata id', () => {
    const minerMetadata = getQsdmSystemTaskMetadata(
      QSDM_MINER_SYSTEM_TASK_METADATA_ID
    );
    const edgeWorkerMetadata = getQsdmSystemTaskMetadata(
      QSDM_EDGE_WORKER_SYSTEM_TASK_METADATA_ID
    );
    const gpuWorkerMetadata = getQsdmSystemTaskMetadata(
      QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_METADATA_ID
    );
    const ramWorkerMetadata = getQsdmSystemTaskMetadata(
      QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_METADATA_ID
    );
    const motherHiveMetadata = getQsdmSystemTaskMetadata(
      QSDM_MOTHER_HIVE_SYSTEM_TASK_METADATA_ID
    );
    const skyFangLinkMetadata = getQsdmSystemTaskMetadata(
      QSDM_SKYFANG_LINK_SYSTEM_TASK_METADATA_ID
    );

    expect(getQsdmSystemTaskById(QSDM_MINER_SYSTEM_TASK_ID)?.task_name).toBe(
      'QSDM Miner'
    );
    expect(minerMetadata?.repositoryUrl).toContain(
      '/QSDM/source/cmd/qsdmminer-console'
    );
    expect(minerMetadata?.infoUrl).toBe(
      'https://qsdm.tech/docs/#/miner-quickstart'
    );
    expect(minerMetadata?.tags).toEqual([
      'QSDM',
      'CELL',
      'Miner',
      'NVIDIA GPU Required',
      'Protocol Emission',
      'No Hive Bounty',
      'CC 7.5+',
      'No Expiry',
    ]);
    expect(minerMetadata?.description).toContain(
      'Sky Fang is a separate integration and is not required for mining'
    );
    expect(minerMetadata?.requirementsTags).not.toEqual(
      expect.arrayContaining([
        expect.objectContaining({ value: 'Sky Fang wallet link' }),
      ])
    );
    expect(minerMetadata?.requirementsTags).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          type: 'ADDON',
          value: 'Protocol mining emission',
        }),
      ])
    );
    expect(
      getQsdmSystemTaskById(QSDM_EDGE_WORKER_SYSTEM_TASK_ID)?.task_name
    ).toBe('QSDM Edge Worker CPU');
    expect(edgeWorkerMetadata?.repositoryUrl).toBe(
      'https://github.com/blackbeardONE/QSDM/tree/main/apps'
    );
    expect(edgeWorkerMetadata?.infoUrl).toBe(
      'https://qsdm.tech/docs/#/qsdm-hive'
    );
    expect(edgeWorkerMetadata?.tags).toEqual([
      'QSDM',
      'CELL',
      'CPU',
      'Edge Worker',
      'Pooled Compute',
      'No Expiry',
    ]);
    expect(
      getQsdmSystemTaskById(QSDM_EDGE_WORKER_GPU_SYSTEM_TASK_ID)?.task_name
    ).toBe('QSDM Edge Worker GPU');
    expect(gpuWorkerMetadata?.tags).toEqual([
      'QSDM',
      'CELL',
      'GPU',
      'CUDA',
      'Edge Worker',
      'Pooled Compute',
      'No Expiry',
    ]);
    expect(
      getQsdmSystemTaskById(QSDM_EDGE_WORKER_RAM_SYSTEM_TASK_ID)?.task_name
    ).toBe('QSDM Edge Worker RAM');
    expect(ramWorkerMetadata?.tags).toEqual([
      'QSDM',
      'CELL',
      'RAM',
      'Memory',
      'Edge Worker',
      'Pooled Compute',
      'No Expiry',
    ]);
    expect(
      getQsdmSystemTaskById(QSDM_MOTHER_HIVE_SYSTEM_TASK_ID)?.task_name
    ).toBe('Mother Hive Task');
    expect(motherHiveMetadata?.tags).toEqual([
      'QSDM',
      'CELL',
      'Mother Hive',
      'Relay',
      'Pooled Compute',
      'No Expiry',
    ]);
    expect(
      getQsdmSystemTaskById(QSDM_SKYFANG_LINK_SYSTEM_TASK_ID)?.task_name
    ).toBe('QSDM Sky Fang Link');
    expect(skyFangLinkMetadata?.repositoryUrl).toBe(
      'https://github.com/blackbeardONE/QSDM/tree/main/apps'
    );
    expect(skyFangLinkMetadata?.infoUrl).toBe(
      'https://qsdm.tech/docs/#/sky-fang-online'
    );
    expect(skyFangLinkMetadata?.tags).toEqual([
      'QSDM',
      'CELL',
      'Sky Fang',
      'Wallet Link',
      'Mandatory',
      'Stake Weighted',
      'Ongoing',
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
      skyFangStakeCell: 0,
      inGameStakeCell: 0,
      gameStakeCell: 0,
      totalGameStakeCell: 0,
      rewardRateCell: QSDM_SKYFANG_LINK_GAME_STAKE_REWARD_RATE,
      rewardModel: 'skyfang-hive-combined-stake-v1',
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

  it('does not treat unpaid Sky Fang submissions as completed rewards', () => {
    expect(
      hasRewardedQsdmTaskSubmissionForSender(
        {
          submissions: {
            '27': {
              [linkedSender]: {
                submission_value: 'old-unpaid-proof',
                slot: 100,
                reward_amount: 0,
                claimed: false,
              },
            },
          },
        },
        linkedSender
      )
    ).toBe(false);

    expect(
      getRewardedQsdmTaskSubmissionForSender(
        {
          submissions: {
            '28': {
              [linkedSender]: {
                submission_value: 'paid-proof',
                slot: 101,
                reward_amount: 1,
                claimed: false,
              },
            },
          },
        },
        linkedSender
      )
    ).toEqual({
      round: 28,
      rewardAmount: 1,
      claimed: false,
    });

    expect(
      hasRewardedQsdmTaskSubmissionForSender(
        {
          submissions: {
            '28': {
              [linkedSender]: {
                submission_value: 'paid-proof',
                slot: 101,
                reward_amount: 1,
                claimed: false,
              },
            },
          },
        },
        linkedSender
      )
    ).toBe(true);
  });

  it('does not treat an older Sky Fang reward as the current round reward', () => {
    const task = {
      submissions: {
        '28': {
          [linkedSender]: {
            submission_value: 'old-paid-proof',
            slot: 101,
            reward_amount: 1,
            claimed: true,
          },
        },
      },
    };

    expect(
      getRewardedQsdmTaskSubmissionForSenderRound(task, linkedSender, 28)
    ).toEqual({
      round: 28,
      rewardAmount: 1,
      claimed: true,
    });
    expect(
      getRewardedQsdmTaskSubmissionForSenderRound(task, linkedSender, 29)
    ).toBeNull();
  });

  it('normalizes Hive denomination values back to Core CELL amounts for worker actions', () => {
    expect(normalizeQsdmSystemTaskCoreCellAmount(50_000_000)).toBe(0.05);
    expect(normalizeQsdmSystemTaskCoreCellAmount(1_000_000_000)).toBe(1);
    expect(normalizeQsdmSystemTaskCoreCellAmount(25)).toBe(25);
    expect(normalizeQsdmSystemTaskCoreCellAmount(undefined, 1)).toBe(1);
  });
});
