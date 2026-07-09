import { Task as TaskRaw } from 'models';
import { NetworkUrlType } from 'renderer/features/shared/constants';
import { Task } from 'renderer/types';

import type {
  QsdmCellFaucetClaimRequest,
  QsdmSkyFangLinkCodeRequest,
  QsdmReferralClaimRequest,
  QsdmReferralClaimResponse,
  QsdmReferralRegisterResponse,
  QsdmReferralRegistrationRequest,
  QsdmReferralStatusResponse,
  QsdmSignerWalletCreateRequest,
  QsdmSignerWalletImportRequest,
  QsdmSignedCellLoopRequest,
  QsdmSignedTransactionEnvelope,
  QsdmTaskActionEnvelope,
  QsdmVirtualComputeCancelRequest,
  QsdmVirtualComputeSubmitRequest,
} from 'models/api/qsdm';

/** Utils */
export function parseTask({ data, publicKey }: TaskRaw): Task {
  return { publicKey, ...data };
}

export const getLogs = async (taskAccountPubKey: string, noOfLines = 500) => {
  const logs = await window.main.getTaskLogs({
    taskAccountPubKey,
    noOfLines,
  });
  console.log('--------------- NODE LOGS ----------------');
  console.log(logs);
  console.log('--------------- END OF NODE LOGS ----------------');
  return logs;
};

export const getMainLogs = () => {
  return window.main.getMainLogs({});
};

const createReferralCode = async (walletAddress: string) => {
  return window.main.createReferralCode(walletAddress);
};

export const getReferralCode = async (walletAddress: string) => {
  if (!walletAddress) return;

  return createReferralCode(walletAddress);
};

export const switchLaunchOnRestart = async () => {
  return window.main.switchLaunchOnRestart();
};

export const limitLogsSize = async () => {
  return window.main.limitLogsSize();
};

export const enableStayAwake = async () => {
  return window.main.enableStayAwake();
};

export const disableStayAwake = async () => {
  return window.main.disableStayAwake();
};

export const getTaskMetadata = async (metadataCID: string) => {
  return window.main
    .getTaskMetadata({
      metadataCID,
    })
    .then((metadata) => {
      return metadata;
    });
};

export const switchNetwork = async (network: NetworkUrlType) => {
  return window.main.switchNetwork(network);
};

export const getNetworkUrl = async () => {
  return window.main.getNetworkUrl();
};

export const openLogfileFolder = async (taskPublicKey: string) => {
  if (!taskPublicKey) return false;
  return window.main.openLogfileFolder({
    taskAccountPublicKey: taskPublicKey,
  });
};

export const getActiveAccountName = async () => {
  return window.main.getActiveAccountName();
};

export const getVersion = async () => {
  return window.main.getVersion();
};

export const getPlatform = async () => {
  return window.main.getPlatform();
};

export const getMainAccountPublicKey = async (): Promise<string> => {
  const pubkey = await window.main.getMainAccountPubKey();
  return pubkey;
};

export const openBrowserWindow = async (URL: string) => {
  await window.main.openBrowserWindow({ URL });
};

export const appRelaunch = async () => {
  await window.main.appRelaunch();
};

export const saveTaskThumbnail = async (url: string) => {
  return window.main.saveTaskThumbnail({ url });
};

export const getTaskThumbnail = async (url: string) => {
  return window.main.getTaskThumbnail({ url });
};

export const getRPCStatus = async () => {
  return window.main.getRPCStatus();
};

export const getQsdmCoreStatus = async () => {
  return window.main.getQsdmCoreStatus();
};

export const getQsdmCellAccount = async (address?: string) => {
  return window.main.getQsdmCellAccount(address ? { address } : undefined);
};

export const getQsdmMinerRewardStatus = async () => {
  return window.main.getQsdmMinerRewardStatus();
};

export const getQsdmMotherHiveStatus = async () => {
  return window.main.getQsdmMotherHiveStatus();
};

export const pairQsdmMotherHive = async (pairingCode: string) => {
  return window.main.pairQsdmMotherHive({ pairingCode });
};

export const disconnectQsdmMotherHive = async () => {
  return window.main.disconnectQsdmMotherHive();
};

export const getQsdmVirtualComputeResources = async () => {
  return window.main.getQsdmVirtualComputeResources();
};

export const getQsdmVirtualComputeJobs = async () => {
  return window.main.getQsdmVirtualComputeJobs();
};

export const submitQsdmVirtualComputeJob = async (
  payload: QsdmVirtualComputeSubmitRequest
) => {
  return window.main.submitQsdmVirtualComputeJob(payload);
};

export const cancelQsdmVirtualComputeJob = async (
  payload: QsdmVirtualComputeCancelRequest
) => {
  return window.main.cancelQsdmVirtualComputeJob(payload);
};

export const getQsdmSkyFangLinkStatus = async () => {
  return window.main.getQsdmSkyFangLinkStatus();
};

export const linkQsdmSkyFangAccount = async (
  payload: QsdmSkyFangLinkCodeRequest
) => {
  return window.main.linkQsdmSkyFangAccount(payload);
};

export const setQsdmMinerRewardAddressToSigner = async () => {
  return window.main.setQsdmMinerRewardAddressToSigner();
};

export const createQsdmSignerWallet = async (
  payload: QsdmSignerWalletCreateRequest
) => {
  return window.main.createQsdmSignerWallet(payload);
};

export const importQsdmSignerWallet = async (
  payload: QsdmSignerWalletImportRequest
) => {
  return window.main.importQsdmSignerWallet(payload);
};

export const exportQsdmSignerWalletBackup = async () => {
  return window.main.exportQsdmSignerWalletBackup();
};

export const claimQsdmCellFaucet = async (
  payload?: QsdmCellFaucetClaimRequest
) => {
  return window.main.claimQsdmCellFaucet(payload);
};

export const getQsdmReferralStatus = async (
  referred: string
): Promise<QsdmReferralStatusResponse> => {
  return window.main.getQsdmReferralStatus(referred);
};

export const getQsdmReferralRewardPoolStatus = async () =>
  window.main.getQsdmReferralRewardPoolStatus();

export const registerQsdmReferral = async (
  payload: QsdmReferralRegistrationRequest
): Promise<QsdmReferralRegisterResponse> => {
  return window.main.registerQsdmReferral(payload);
};

export const claimQsdmReferralReward = async (
  payload?: QsdmReferralClaimRequest
): Promise<QsdmReferralClaimResponse> => {
  return window.main.claimQsdmReferralReward(payload);
};

export const submitQsdmSignedTransaction = async (
  envelope: QsdmSignedTransactionEnvelope
) => {
  return window.main.submitQsdmSignedTransaction(envelope);
};

export const submitQsdmTaskAction = async (
  envelope: QsdmTaskActionEnvelope
) => {
  return window.main.submitQsdmTaskAction(envelope);
};

export const runQsdmSignedCellLoop = async (
  payload?: QsdmSignedCellLoopRequest
) => {
  return window.main.runQsdmSignedCellLoop(payload);
};

export const fetchCellPrice = async () => {
  return 0;
};
