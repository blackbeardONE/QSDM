import { execFile, spawn } from 'child_process';
import { createHash, randomBytes } from 'crypto';
import fs from 'fs';
import os from 'os';
import path from 'path';

import axios from 'axios';

import {
  buildQsdmCoreApiUrl,
  getQsdmCoreConnectionMode,
  getQsdmRuntimeCoreApiUrl,
  QSDM_CANONICAL_API_URL,
  QSDM_CORE_CELL_DECIMALS,
  QSDM_GATEWAY_API_URL,
} from 'config/qsdm';
import { assertQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import {
  getQsdmTaskActionCliPath,
  getQsdmTaskActionKeystorePath,
  getQsdmTaskActionPassphraseFile,
  getQsdmTaskActionSender,
} from 'main/services/qsdmTaskActionSigner';
import { QsdmMiningAccountResponse } from 'models/api/qsdm';

const SIGNED_ENROLLMENT_CONTRACT = 'qsdm/enroll/v2';
const ENROLLMENT_FEE_CELL = 0.001;

export const resolveQsdmMinerEnrollmentSubmitApiUrl = ({
  runtimeApiUrl = getQsdmRuntimeCoreApiUrl(),
  gatewayApiUrl = QSDM_GATEWAY_API_URL,
  canonicalApiUrl = QSDM_CANONICAL_API_URL,
}: {
  runtimeApiUrl?: string;
  gatewayApiUrl?: string;
  canonicalApiUrl?: string;
} = {}) => {
  const target =
    getQsdmCoreConnectionMode(runtimeApiUrl, gatewayApiUrl) === 'gateway'
      ? canonicalApiUrl
      : runtimeApiUrl;
  return target.replace(/\/+$/, '');
};

type CoreMiningStatus = {
  protocol_versions_accepted?: number[];
  fork_v2_tc_active?: boolean;
  fork_v2_tc_height?: number;
  min_enroll_stake_dust?: number;
  enrollment_contract?: string;
  signed_enrollment_required?: boolean;
  signed_enrollment_activation_height?: number;
  deferred_bond_from_rewards?: boolean;
  deferred_bond_activation_height?: number;
  deferred_bond_work_difficulty?: number;
};

type CoreStatus = {
  chain_tip?: number;
  mining?: CoreMiningStatus;
};

type EnrollmentRecord = {
  node_id: string;
  owner: string;
  gpu_uuid: string;
  stake_dust: number;
  bond_mode?: 'upfront' | 'mining_rewards';
  required_stake_dust?: number;
  bond_remaining_dust?: number;
  fully_bonded?: boolean;
  phase: 'active' | 'pending_unbond' | 'revoked';
  slashable: boolean;
  enrolled_at_height?: number;
  unbond_matures_at_height?: number;
};

export type QsdmNvidiaGPU = {
  uuid: string;
  name: string;
  computeCapability: string;
  driverVersion: string;
  cudaVersion: string;
  architecture: string;
};

export type QsdmMinerIdentity = QsdmNvidiaGPU & {
  nodeId: string;
  hmacKeyPath: string;
  configPath: string;
};

export type QsdmMinerEnrollmentStatus = {
  configured: boolean;
  eligible: boolean;
  ready: boolean;
  signerAddress?: string;
  nodeId?: string;
  gpu?: QsdmNvidiaGPU;
  phase?: EnrollmentRecord['phase'];
  enrolledOwner?: string;
  enrolledGpuUuid?: string;
  enrollmentMatchesSigner: boolean;
  enrollmentMatchesGpu: boolean;
  requiredStakeDust: number;
  requiredStakeCell: number;
  bondedStakeDust?: number;
  bondedStakeCell?: number;
  bondMode?: 'upfront' | 'mining_rewards';
  bondRemainingDust?: number;
  bondRemainingCell?: number;
  fullyBonded?: boolean;
  deferredBondAvailable: boolean;
  deferredBondActivationHeight?: number;
  balanceCell?: number;
  contract?: string;
  activationHeight?: number;
  computeBackend?: 'cpu-reference' | 'cuda';
  gpuComputeActive?: boolean;
  tensorCoreForkActive?: boolean;
  tensorCoreForkHeight?: number;
  error?: string;
};

const execFileText = (command: string, args: string[], timeout = 7000) =>
  new Promise<string>((resolve, reject) => {
    execFile(
      command,
      args,
      { windowsHide: true, timeout, maxBuffer: 1024 * 1024 },
      (error, stdout) => {
        if (error) {
          reject(error);
          return;
        }
        resolve(stdout.trim());
      }
    );
  });

export const inferQsdmNvidiaArchitecture = (computeCapability: string) => {
  const value = Number.parseFloat(computeCapability);
  if (!Number.isFinite(value)) return '';
  if (value >= 10) return 'blackwell';
  if (value >= 9) return 'hopper';
  if (value >= 8.9) return 'ada-lovelace';
  if (value >= 8) return 'ampere';
  if (value >= 7.5) return 'turing';
  return '';
};

export const parseQsdmNvidiaSmiCsv = (
  output: string,
  cudaVersion = ''
): QsdmNvidiaGPU => {
  const firstGPU = output.split(/\r?\n/).find((line) => line.trim());
  const fields = firstGPU?.split(',').map((value) => value.trim()) || [];
  if (fields.length < 4) {
    throw new Error(
      'nvidia-smi did not return UUID, name, compute capability, and driver version.'
    );
  }
  const [uuid, name, computeCapability, driverVersion] = fields;
  const architecture = inferQsdmNvidiaArchitecture(computeCapability);
  if (!architecture) {
    throw new Error(
      `GPU ${name || uuid} has CUDA compute capability ${
        computeCapability || 'unknown'
      }. QSDM protocol mining requires NVIDIA Turing or newer (7.5+).`
    );
  }
  return {
    uuid,
    name,
    computeCapability,
    driverVersion,
    cudaVersion,
    architecture,
  };
};

const readCudaVersion = async () => {
  try {
    const output = await execFileText('nvidia-smi', []);
    return output.match(/CUDA Version:\s*([0-9.]+)/i)?.[1] || '';
  } catch {
    return '';
  }
};

let cachedGPU: { value: QsdmNvidiaGPU; expiresAt: number } | undefined;

export const detectQsdmNvidiaGPU = async (): Promise<QsdmNvidiaGPU> => {
  if (cachedGPU && cachedGPU.expiresAt > Date.now()) return cachedGPU.value;

  let output = '';
  try {
    output = await execFileText('nvidia-smi', [
      '--query-gpu=uuid,name,compute_cap,driver_version',
      '--format=csv,noheader,nounits',
    ]);
  } catch (error: any) {
    throw new Error(
      `NVIDIA GPU detection failed. Install a current NVIDIA driver and confirm nvidia-smi works. ${
        error?.message || error
      }`
    );
  }

  const value = parseQsdmNvidiaSmiCsv(output, await readCudaVersion());
  cachedGPU = { value, expiresAt: Date.now() + 30_000 };
  return value;
};

const getMinerConfigPath = () =>
  process.env.QSDM_MINER_CONFIG ||
  path.join(os.homedir(), '.qsdm', 'miner.toml');

const readConfigValue = (config: string, key: string) => {
  const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = config.match(
    new RegExp(
      `^\\s*${escaped}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|([^#\\r\\n]+))`,
      'm'
    )
  );
  const value = (match?.[1] || match?.[2] || match?.[3] || '').trim();
  return match?.[1] !== undefined
    ? value.replace(/\\"/g, '"').replace(/\\\\/g, '\\')
    : value;
};

const escapeToml = (value: string) =>
  value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');

const setConfigValue = (config: string, key: string, value: string) => {
  const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const line = `${key} = "${escapeToml(value)}"`;
  const matcher = new RegExp(
    `^\\s*${escaped}\\s*=\\s*(?:"[^"]*"|'[^']*'|[^#\\r\\n]*)`,
    'm'
  );
  if (matcher.test(config)) return config.replace(matcher, line);
  return `${config}${
    config.trim() && !config.endsWith('\n') ? '\n' : ''
  }${line}\n`;
};

export const buildQsdmMinerNodeId = (
  hostname: string,
  gpuUUID: string,
  signerAddress: string
) => {
  const host =
    hostname
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 28) || 'hive';
  const suffix = createHash('sha256')
    .update(`${hostname}:${gpuUUID}:${signerAddress.toLowerCase()}`)
    .digest('hex')
    .slice(0, 12);
  return `hive-${host}-${suffix}`.slice(0, 64);
};

const deterministicNodeId = (gpuUUID: string, signerAddress: string) =>
  buildQsdmMinerNodeId(os.hostname(), gpuUUID, signerAddress);

const isCanonicalQsdmWalletAddress = (address?: string) =>
  /^[0-9a-f]{64}$/.test(address || '');

const ensurePrivateHmacKey = (configuredPath?: string) => {
  const keyPath =
    configuredPath || path.join(os.homedir(), '.qsdm', 'miner-hmac.key');
  fs.mkdirSync(path.dirname(keyPath), { recursive: true, mode: 0o700 });
  if (!fs.existsSync(keyPath)) {
    fs.writeFileSync(keyPath, `${randomBytes(32).toString('hex')}\n`, {
      encoding: 'utf8',
      mode: 0o600,
      flag: 'wx',
    });
  }
  const key = fs.readFileSync(keyPath, 'utf8').trim();
  if (!/^[0-9a-fA-F]{64,256}$/.test(key) || key.length % 2 !== 0) {
    throw new Error(
      `Miner HMAC key file ${keyPath} is malformed; expected 32-128 bytes of hex.`
    );
  }
  try {
    fs.chmodSync(keyPath, 0o600);
  } catch {
    // Windows enforces the current user profile ACL instead of POSIX mode bits.
  }
  return keyPath;
};

const validatorBaseUrl = () =>
  getQsdmRuntimeCoreApiUrl().replace(/\/api\/v1\/?$/i, '');

const readConfiguredIdentity = (): QsdmMinerIdentity | undefined => {
  const configPath = getMinerConfigPath();
  if (!fs.existsSync(configPath)) return undefined;
  const config = fs.readFileSync(configPath, 'utf8');
  const protocol = readConfigValue(config, 'protocol');
  const nodeId = readConfigValue(config, 'node_id');
  const uuid = readConfigValue(config, 'gpu_uuid');
  const hmacKeyPath = readConfigValue(config, 'hmac_key_path');
  if (
    protocol.toLowerCase() !== 'v2' ||
    !nodeId ||
    !uuid ||
    !hmacKeyPath ||
    !fs.existsSync(hmacKeyPath)
  ) {
    return undefined;
  }
  return {
    nodeId,
    uuid,
    hmacKeyPath,
    configPath,
    name: readConfigValue(config, 'gpu_name'),
    architecture: readConfigValue(config, 'gpu_arch'),
    computeCapability: readConfigValue(config, 'compute_cap'),
    cudaVersion: readConfigValue(config, 'cuda_version'),
    driverVersion: readConfigValue(config, 'driver_ver'),
  };
};

export const prepareQsdmMinerV2Config =
  async (): Promise<QsdmMinerIdentity> => {
    const signer = getQsdmTaskActionSender();
    if (!signer)
      throw new Error(
        'Create or import a QSDM signer wallet before configuring mining.'
      );
    const gpu = await detectQsdmNvidiaGPU();
    const configPath = getMinerConfigPath();
    fs.mkdirSync(path.dirname(configPath), { recursive: true, mode: 0o700 });
    const previous = fs.existsSync(configPath)
      ? fs.readFileSync(configPath, 'utf8')
      : '';
    const nodeId =
      readConfigValue(previous, 'node_id') ||
      deterministicNodeId(gpu.uuid, signer);
    const hmacKeyPath = ensurePrivateHmacKey(
      readConfigValue(previous, 'hmac_key_path')
    );
    const values: Record<string, string> = {
      protocol: 'v2',
      node_id: nodeId,
      gpu_uuid: gpu.uuid,
      gpu_name: gpu.name,
      gpu_arch: gpu.architecture,
      compute_cap: gpu.computeCapability,
      cuda_version: gpu.cudaVersion,
      driver_ver: gpu.driverVersion,
      hmac_key_path: hmacKeyPath,
      reward_address: signer,
      validator_url: validatorBaseUrl(),
    };
    const updated = Object.entries(values).reduce(
      (config, [key, value]) => setConfigValue(config, key, value),
      previous
    );
    if (updated !== previous && fs.existsSync(configPath)) {
      fs.copyFileSync(configPath, `${configPath}.bak-${Date.now()}`);
    }
    if (updated !== previous)
      fs.writeFileSync(configPath, updated, { encoding: 'utf8', mode: 0o600 });
    return { ...gpu, nodeId, hmacKeyPath, configPath };
  };

const rotateLegacyMinerNodeId = (
  identity: QsdmMinerIdentity,
  signerAddress: string
): QsdmMinerIdentity => {
  const nodeId = deterministicNodeId(identity.uuid, signerAddress);
  if (nodeId === identity.nodeId) return identity;

  const previous = fs.readFileSync(identity.configPath, 'utf8');
  const updated = setConfigValue(previous, 'node_id', nodeId);
  fs.copyFileSync(
    identity.configPath,
    `${identity.configPath}.bak-${Date.now()}`
  );
  fs.writeFileSync(identity.configPath, updated, {
    encoding: 'utf8',
    mode: 0o600,
  });
  return { ...identity, nodeId };
};

const fetchEnrollment = async (nodeId: string) => {
  try {
    const response = await axios.get<EnrollmentRecord>(
      buildQsdmCoreApiUrl(`/mining/enrollment/${encodeURIComponent(nodeId)}`),
      { timeout: 10_000 }
    );
    return response.data;
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 404)
      return undefined;
    throw error;
  }
};

export const getQsdmMinerEnrollmentStatus =
  async (): Promise<QsdmMinerEnrollmentStatus> => {
    const signerAddress = getQsdmTaskActionSender();
    let identity = readConfiguredIdentity();
    let gpu: QsdmNvidiaGPU | undefined;
    try {
      gpu = await detectQsdmNvidiaGPU();
    } catch (error: any) {
      return {
        configured: Boolean(identity),
        eligible: false,
        ready: false,
        computeBackend: 'cuda',
        gpuComputeActive: false,
        signerAddress,
        nodeId: identity?.nodeId,
        enrollmentMatchesSigner: false,
        enrollmentMatchesGpu: false,
        requiredStakeDust: 0,
        requiredStakeCell: 0,
        deferredBondAvailable: false,
        error: error?.message || String(error),
      };
    }
    if (!identity) {
      identity = {
        ...gpu,
        nodeId: deterministicNodeId(gpu.uuid, signerAddress || ''),
        hmacKeyPath: path.join(os.homedir(), '.qsdm', 'miner-hmac.key'),
        configPath: getMinerConfigPath(),
      };
    }

    try {
      const [statusResponse, accountResponse, enrollmentRecord] =
        await Promise.all([
          axios.get<CoreStatus>(buildQsdmCoreApiUrl('/status'), {
            timeout: 10_000,
          }),
          signerAddress
            ? axios.get<QsdmMiningAccountResponse>(
                buildQsdmCoreApiUrl('/mining/account'),
                {
                  params: { address: signerAddress },
                  timeout: 10_000,
                }
              )
            : Promise.resolve(undefined),
          fetchEnrollment(identity.nodeId),
        ]);
      const mining = statusResponse.data.mining;
      const requiredStakeDust = Number(mining?.min_enroll_stake_dust || 0);
      const factor = 10 ** QSDM_CORE_CELL_DECIMALS;
      const enrollmentMatchesSigner = Boolean(
        signerAddress &&
          enrollmentRecord?.owner?.toLowerCase() === signerAddress.toLowerCase()
      );
      const enrollmentMatchesGpu = Boolean(
        enrollmentRecord?.gpu_uuid?.toLowerCase() === gpu.uuid.toLowerCase()
      );
      const signedContractReady =
        mining?.enrollment_contract === SIGNED_ENROLLMENT_CONTRACT &&
        mining?.signed_enrollment_required === true;
      const ready = Boolean(
        signedContractReady &&
          enrollmentRecord?.phase === 'active' &&
          enrollmentMatchesSigner &&
          enrollmentMatchesGpu
      );
      return {
        configured: Boolean(readConfiguredIdentity()),
        eligible: true,
        ready,
        signerAddress,
        nodeId: identity.nodeId,
        gpu,
        phase: enrollmentRecord?.phase,
        enrolledOwner: enrollmentRecord?.owner,
        enrolledGpuUuid: enrollmentRecord?.gpu_uuid,
        enrollmentMatchesSigner,
        enrollmentMatchesGpu,
        requiredStakeDust,
        requiredStakeCell: requiredStakeDust / factor,
        bondedStakeDust: enrollmentRecord?.stake_dust,
        bondedStakeCell: enrollmentRecord
          ? enrollmentRecord.stake_dust / factor
          : undefined,
        bondMode: enrollmentRecord?.bond_mode,
        bondRemainingDust: enrollmentRecord?.bond_remaining_dust,
        bondRemainingCell:
          enrollmentRecord?.bond_remaining_dust !== undefined
            ? enrollmentRecord.bond_remaining_dust / factor
            : undefined,
        fullyBonded: enrollmentRecord?.fully_bonded,
        deferredBondAvailable: mining?.deferred_bond_from_rewards === true,
        deferredBondActivationHeight: mining?.deferred_bond_activation_height,
        balanceCell: accountResponse?.data.balance,
        contract: mining?.enrollment_contract,
        activationHeight: mining?.signed_enrollment_activation_height,
        computeBackend: 'cuda',
        gpuComputeActive: false,
        tensorCoreForkActive: mining?.fork_v2_tc_active === true,
        tensorCoreForkHeight: mining?.fork_v2_tc_height,
        error: signedContractReady
          ? undefined
          : 'QSDM Core must be updated to signed miner enrollment v2 before this miner can start.',
      };
    } catch (error: any) {
      return {
        configured: Boolean(readConfiguredIdentity()),
        eligible: true,
        ready: false,
        signerAddress,
        nodeId: identity.nodeId,
        gpu,
        enrollmentMatchesSigner: false,
        enrollmentMatchesGpu: false,
        requiredStakeDust: 0,
        requiredStakeCell: 0,
        deferredBondAvailable: false,
        computeBackend: 'cuda',
        gpuComputeActive: false,
        error: error?.message || String(error),
      };
    }
  };

const runEnrollmentCli = async (
  identity: QsdmMinerIdentity,
  requiredStakeDust: number,
  bondMode: 'upfront' | 'mining_rewards'
) => {
  const cliPath = getQsdmTaskActionCliPath();
  const keystorePath = getQsdmTaskActionKeystorePath();
  const passphraseFile = getQsdmTaskActionPassphraseFile();
  const sender = getQsdmTaskActionSender();
  if (!cliPath || !keystorePath || !passphraseFile || !sender) {
    throw new Error(
      'QSDM signer CLI, keystore, and passphrase file are required for enrollment.'
    );
  }
  // The official home gateway intentionally exposes a narrow consumer API.
  // Signed qsdm/enroll/v2 transactions go to canonical Core, which verifies
  // ML-DSA ownership and enrollment work before gossiping them to validators.
  const submitApiUrl = resolveQsdmMinerEnrollmentSubmitApiUrl();
  const account = await axios.get<QsdmMiningAccountResponse>(
    `${submitApiUrl}/mining/account`,
    { params: { address: sender }, timeout: 10_000 }
  );
  const args = [
    'enroll',
    '--in',
    keystorePath,
    '--passphrase-file',
    passphraseFile,
    '--sender',
    sender,
    '--node-id',
    identity.nodeId,
    '--gpu-uuid',
    identity.uuid,
    '--hmac-key-file',
    identity.hmacKeyPath,
    '--nonce',
    String(account.data.nonce),
    '--fee',
    String(bondMode === 'mining_rewards' ? 0 : ENROLLMENT_FEE_CELL),
    '--memo',
    'QSDM Hive NVIDIA miner enrollment',
  ];
  if (bondMode === 'mining_rewards') {
    args.push('--bond-from-rewards');
  } else {
    args.push('--stake', String(requiredStakeDust));
  }
  await new Promise<void>((resolve, reject) => {
    const child = spawn(cliPath, args, {
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, QSDM_API_URL: submitApiUrl },
    });
    let stderr = '';
    const timeout = setTimeout(() => child.kill(), 90_000);
    child.stderr?.on('data', (chunk) => {
      stderr += chunk.toString();
    });
    child.on('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    child.on('close', (code) => {
      clearTimeout(timeout);
      if (code === 0) resolve();
      else
        reject(
          new Error(
            `Signed miner enrollment was rejected: ${
              stderr.trim() || `qsdmcli exit ${code}`
            }`
          )
        );
    });
  });
};

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export const enrollQsdmMiner = async (
  bondMode: 'upfront' | 'mining_rewards' = 'upfront'
): Promise<QsdmMinerEnrollmentStatus> => {
  await assertQsdmCanonicalChainSafety();
  let identity = await prepareQsdmMinerV2Config();
  let before = await getQsdmMinerEnrollmentStatus();
  if (before.ready) return before;

  if (
    before.enrolledOwner &&
    !before.enrollmentMatchesSigner &&
    !isCanonicalQsdmWalletAddress(before.enrolledOwner) &&
    before.signerAddress
  ) {
    identity = rotateLegacyMinerNodeId(identity, before.signerAddress);
    before = await getQsdmMinerEnrollmentStatus();
  }
  if (before.contract !== SIGNED_ENROLLMENT_CONTRACT) {
    throw new Error(
      before.error || 'QSDM Core does not support signed miner enrollment v2.'
    );
  }
  if (!before.requiredStakeDust)
    throw new Error('QSDM Core did not advertise a miner enrollment bond.');
  if (bondMode === 'mining_rewards' && !before.deferredBondAvailable) {
    const activation = before.deferredBondActivationHeight
      ? ` Activation height: ${before.deferredBondActivationHeight}.`
      : '';
    throw new Error(
      `QSDM Core has not activated bond from mining rewards yet.${activation}`
    );
  }
  if (
    bondMode === 'upfront' &&
    (before.balanceCell || 0) < before.requiredStakeCell + ENROLLMENT_FEE_CELL
  ) {
    throw new Error(
      `Miner enrollment needs ${
        before.requiredStakeCell
      } CELL plus the ${ENROLLMENT_FEE_CELL} CELL transaction fee. Current signer balance: ${
        before.balanceCell || 0
      } CELL.`
    );
  }
  if (before.phase === 'pending_unbond') {
    throw new Error(
      'This miner identity is still in its unbonding period and cannot be re-enrolled yet.'
    );
  }
  if (before.enrolledOwner && !before.enrollmentMatchesSigner) {
    throw new Error(
      'This miner NodeID is enrolled to a different QSDM wallet. Refusing to replace it.'
    );
  }
  if (before.enrolledGpuUuid && !before.enrollmentMatchesGpu) {
    throw new Error(
      'This miner NodeID is enrolled to a different NVIDIA GPU. Refusing to replace it.'
    );
  }

  await runEnrollmentCli(identity, before.requiredStakeDust, bondMode);
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    await delay(2_000);
    const current = await getQsdmMinerEnrollmentStatus();
    if (current.ready) return current;
  }
  throw new Error(
    'Signed enrollment was submitted but was not confirmed within 120 seconds. Check Core connectivity and retry.'
  );
};

export const assertQsdmMinerEnrollmentReady = async () => {
  const status = await getQsdmMinerEnrollmentStatus();
  if (!status.ready) {
    throw new Error(
      status.error ||
        'QSDM miner enrollment is not active for this signer and GPU.'
    );
  }
  return status;
};
