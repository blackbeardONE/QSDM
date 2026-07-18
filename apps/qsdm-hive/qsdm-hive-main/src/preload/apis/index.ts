import appRelaunch from './appRelaunch';
import { checkAppUpdate, onAppDownloaded, onAppUpdate } from './appUpdates';
import archiveTask from './archiveTask';
import { resetPin } from './auth';
import { fetchS3FolderContents } from './aws';
import {
  copyBrandingFolder,
  getBrandingFolderPath,
  getBrandingConfig,
  getBrandLogo,
  validateBrandingFolder,
} from './branding';
import cancelTaskRetry from './cancelTaskRetry';
import checkIfStakingAccountIsFaulty from './checkIfStakingAccountIsFaulty';
import checkVipAccess from './checkVipAccess';
import checkWalletExists from './checkWalletExists';
import claimQsdmCellFaucet from './claimQsdmCellFaucet';
import claimQsdmReferralReward from './claimQsdmReferralReward';
import claimReward from './claimReward';
import claimRewardKPL from './claimRewardKPL';
import copyTextToClipboard from './copyTextToClipboard';
import createMissingKPLStakingKey from './createMissingKPLStakingKey';
import createNodeWallets from './createNodeWallets';
import createNodeWalletsFromJson from './createNodeWalletsFromJson';
import createQsdmSignerWallet from './createQsdmSignerWallet';
import createReferralCode from './createReferralCode';
import creditStakingWalletFromMainWallet from './creditStakingWalletFromMainWallet';
import delegateStake from './delegateStake';
import disableStayAwake from './disableStayAwake';
import downloadAppUpdate from './downloadAppUpdate';
import enableStayAwake from './enableStayAwake';
import exportQsdmSignerWalletBackup from './exportQsdmSignerWalletBackup';
import { verifyMessage } from './faucet';
import fetchKPLTokenMetadata from './fetchKPLTokenMetadata';
import fetchMultipleKPLTokenMetadata from './fetchMultipleKPLTokenMetadata';
import generateSeedPhrase from './generateSeedPhrase';
import getAccountBalance from './getAccountBalance';
import getActiveAccountName from './getActiveAccountName';
import getAllAccounts from './getAllAccounts';
import getAllTimeRewardsByTask from './getAllTimeRewardsByTask';
import getAvailableTasks from './getAvailableTasks';
import getAverageSlotTime from './getAverageSlotTime';
import getCurrentSlot from './getCurrentSlot';
import getHiveVersionPolicy from './getHiveVersionPolicy';
import getIsAccountHidden from './getIsAccountHidden';
import getIsTaskRunning from './getIsTaskRunning';
import getKPLStakingAccountPubKey from './getKPLStakingAccountPubKey';
import getLastSubmissionTime from './getLastSubmissionTime';
import getMainAccountPubKey from './getMainAccountPubKey';
import getMainLogs from './getMainLogs';
import getMyTasks from './getMyTasks';
import getNetworkUrl from './getNetworkUrl';
import { getOperativeSystem } from './getOperativeSystem';
import getPlatform from './getPlatform';
import getPrivateTasks from './getPrivateTasks';
import getQsdmCellAccount from './getQsdmCellAccount';
import getQsdmCoreStatus from './getQsdmCoreStatus';
import getQsdmMinerRewardStatus from './getQsdmMinerRewardStatus';
import getQsdmMotherHiveStatus from './getQsdmMotherHiveStatus';
import {
  getQsdmReferralRewardPoolStatus,
  getQsdmReferralStatus,
} from './getQsdmReferralStatus';
import getQsdmSkyFangLinkStatus from './getQsdmSkyFangLinkStatus';
import getRentAmount from './getRentAmount';
import getRetryDataByTaskId from './getRetryDataByTaskId';
import getRPCStatus from './getRPCStatus';
import getRunningTasksPubKeys from './getRunningTasksPubKeys';
import getStakingAccountPubKey from './getStakingAccountPubKey';
import getStartedTasksPubKeys from './getStartedTasksPubKeys';
import getTaskInfo from './getTaskInfo';
import getTaskLogs from './getTaskLogs';
import getTaskMetadata from './getTaskMetadata';
import getTaskNodeInfo from './getTaskNodeInfo';
import getTasksById from './getTasksById';
import getTaskSource from './getTaskSource';
import getTaskSubmissions from './getTaskSubmissions';
import getTaskThumbnail from './getTaskThumbnail';
import getTaskVariablesNames from './getTaskVariablesNames';
import getTimeToNextReward from './getTimeToNextReward';
import getUserConfig from './getUserConfig';
import getVersion from './getVersion';
import importQsdmSignerWallet from './importQsdmSignerWallet';
import initializeTasks from './initializeTasks';
import isValidWalletAddress from './isValidWalletAddress';
import { fetchKPLList, getKPLBalance, transferKplToken } from './kplTokens';
import linkQsdmSkyFangAccount from './linkQsdmSkyFangAccount';
import { limitLogsSize } from './logger';
import manageQsdmTaskCatalog from './manageQsdmTaskCatalog';
import {
  finishNetworkMigration,
  startNetworkMigration,
} from './networkMigration';
import {
  getNotificationsFromStore,
  purgeNotifications,
  removeNotification,
  storeNotification,
  updateNotification,
} from './notifications';
import onDeepLinkNavigation from './onDeepLinkNavigation';
import onK2ConnectionError from './onK2ConnectionError';
import onSystemWakeUp from './onSystemWakeUp';
import onVariablesUpdatedFromMainProcess from './onVariablesUpdatedFromMainProcess';
import openBrowserWindow from './openBrowserWindow';
import openLogfileFolder from './openLogfileFolder';
import openNodeLogfileFolder from './openNodeLogfileFolder';
import {
  disconnectQsdmMotherHive,
  pairQsdmMotherHive,
} from './pairQsdmMotherHive';
import { getRunnedPrivateTasks, setRunnedPrivateTasks } from './privateTasks';
import {
  cancelQsdmVirtualComputeJob,
  getQsdmVirtualComputeJobs,
  getQsdmVirtualComputeResources,
  submitQsdmVirtualComputeJob,
} from './qsdmVirtualCompute';
import quitApp from './quitApp';
import recoverKPLStakingAccount from './recoverKPLStakingAccount';
import recoverLostTokens from './recoverLostTokens';
import recoverStakingAccount from './recoverStakingAccount';
import redeemTokensInNewNetwork from './redeemTokensInNewNetwork';
import registerQsdmReferral from './registerQsdmReferral';
import removeAccountByName from './removeAccountByName';
import runQsdmSignedCellLoop from './runQsdmSignedCellLoop';
import saveTaskThumbnail from './saveTaskThumbnail';
import {
  getEncryptedSecretPhrase,
  getEncryptedSecretPhraseMap,
  saveEncryptedSecretPhraseMap,
} from './security';
import setActiveAccount from './setActiveAccount';
import setIsAccountHidden from './setIsAccountHidden';
import setQsdmMinerRewardAddressToSigner from './setQsdmMinerRewardAddressToSigner';
import setZoom from './setZoom';
import startAllTasks from './startAllTasks';
import startTask from './startTask';
import stopAllTasks from './stopAllTasks';
import stopTask from './stopTask';
import storeAllTimeRewards from './storeAllTimeRewards';
import storeUserConfig from './storeUserConfig';
import submitQsdmSignedTransaction from './submitQsdmSignedTransaction';
import submitQsdmTaskAction from './submitQsdmTaskAction';
import switchLaunchOnRestart from './switchLaunchOnRestart';
import switchNetwork from './switchNetwork';
import {
  getMyTaskStake,
  getMyTaskSubmissionRoundInfo,
  getTaskByTaskAuditProgramId,
  onTaskExecutableFileChange,
  onTaskNotificationReceived,
  resetTasksCache,
} from './tasks';
import {
  addSession,
  addTaskToScheduler,
  getAllSessions,
  getSchedulerTasks,
  getSessionById,
  removeSession,
  removeTaskFromScheduler,
  updateSessionById,
  validateSchedulerSession,
} from './tasksScheduler';
import {
  deleteTaskVariable,
  editTaskVariable,
  getStoredPairedTaskVariables,
  getStoredTaskVariables,
  getTaskPairedVariablesNamesWithLabels,
  getTasksPairedWithVariable,
  pairTaskVariable,
  storeTaskVariable,
} from './taskVariables';
import toggleTheme from './toggleTheme';
import {
  transferCellFromMainWallet,
  transferCellFromStakingWallet,
} from './transferQsdmHive';
import upgradeTask from './upgradeTask';
import { checkUPnPBinary, fetchAndSaveUPnPBinary } from './upnp';
import validateBip39Word from './validateBip39Word';
import withdrawStake from './withdrawStake';

export default {
  getBrandLogo,
  getBrandingFolderPath,
  validateBrandingFolder,
  copyBrandingFolder,
  copyTextToClipboard,
  getBrandingConfig,
  toggleTheme,
  getOperativeSystem,
  onVariablesUpdatedFromMainProcess,
  createReferralCode,
  setIsAccountHidden,
  getIsAccountHidden,
  getPrivateTasks,
  getQsdmCellAccount,
  getQsdmMinerRewardStatus,
  getQsdmMotherHiveStatus,
  pairQsdmMotherHive,
  disconnectQsdmMotherHive,
  getQsdmVirtualComputeResources,
  getQsdmVirtualComputeJobs,
  submitQsdmVirtualComputeJob,
  cancelQsdmVirtualComputeJob,
  getQsdmSkyFangLinkStatus,
  linkQsdmSkyFangAccount,
  createQsdmSignerWallet,
  exportQsdmSignerWalletBackup,
  importQsdmSignerWallet,
  claimQsdmCellFaucet,
  getQsdmReferralStatus,
  getQsdmReferralRewardPoolStatus,
  registerQsdmReferral,
  claimQsdmReferralReward,
  getQsdmCoreStatus,
  recoverKPLStakingAccount,
  fetchKPLTokenMetadata,
  fetchMultipleKPLTokenMetadata,
  setZoom,
  createMissingKPLStakingKey,
  claimRewardKPL,
  checkIfStakingAccountIsFaulty,
  getTaskSource,
  getTaskMetadata,
  getTaskInfo,
  delegateStake,
  startTask,
  stopTask,
  checkWalletExists,
  getMainAccountPubKey,
  getTaskLogs,
  getStakingAccountPubKey,
  withdrawStake,
  getMyTasks,
  getTaskVariablesNames,
  getAvailableTasks,
  claimReward,
  createNodeWallets,
  createNodeWalletsFromJson,
  generateSeedPhrase,
  setActiveAccount,
  getAllAccounts,
  getActiveAccountName,
  storeUserConfig,
  getUserConfig,
  getHiveVersionPolicy,
  getTasksById,
  removeAccountByName,
  openBrowserWindow,
  getTaskNodeInfo,
  getStoredTaskVariables,
  storeTaskVariable,
  onDeepLinkNavigation,
  editTaskVariable,
  deleteTaskVariable,
  pairTaskVariable,
  getStoredPairedTaskVariables,
  getTaskPairedVariablesNamesWithLabels,
  getTasksPairedWithVariable,
  getAccountBalance,
  switchNetwork,
  getNetworkUrl,
  initializeTasks,
  getCurrentSlot,
  getAverageSlotTime,
  openLogfileFolder,
  openNodeLogfileFolder,
  getVersion,
  getPlatform,
  getEncryptedSecretPhrase,
  getEncryptedSecretPhraseMap,
  archiveTask,
  downloadAppUpdate,
  onAppUpdate,
  getAllTimeRewardsByTask,
  storeAllTimeRewards,
  submitQsdmTaskAction,
  manageQsdmTaskCatalog,
  submitQsdmSignedTransaction,
  setQsdmMinerRewardAddressToSigner,
  runQsdmSignedCellLoop,
  isValidWalletAddress,
  getRunnedPrivateTasks,
  setRunnedPrivateTasks,
  getIsTaskRunning,
  disableStayAwake,
  enableStayAwake,
  onSystemWakeUp,
  getMainLogs,
  onAppDownloaded,
  checkAppUpdate,
  getStartedTasksPubKeys,
  upgradeTask,
  getRunningTasksPubKeys,
  getTimeToNextReward,
  cancelTaskRetry,
  getRetryDataByTaskId,
  switchLaunchOnRestart,
  stopAllTasks,
  startAllTasks,
  addSession,
  removeSession,
  updateSessionById,
  getAllSessions,
  getSessionById,
  addTaskToScheduler,
  getSchedulerTasks,
  removeTaskFromScheduler,
  validateSchedulerSession,
  creditStakingWalletFromMainWallet,
  limitLogsSize,
  getLastSubmissionTime,
  onTaskExecutableFileChange,
  redeemTokensInNewNetwork,
  transferCellFromMainWallet,
  transferCellFromStakingWallet,
  getRentAmount,
  storeNotification,
  getNotificationsFromStore,
  purgeNotifications,
  removeNotification,
  updateNotification,
  fetchS3FolderContents,
  saveEncryptedSecretPhraseMap,
  appRelaunch,
  fetchAndSaveUPnPBinary,
  checkUPnPBinary,
  validateBip39Word,
  startNetworkMigration,
  finishNetworkMigration,
  getTaskByTaskAuditProgramId,
  getTaskThumbnail,
  saveTaskThumbnail,
  onTaskNotificationReceived,
  getMyTaskStake,
  getMyTaskSubmissionRoundInfo,
  getRPCStatus,
  recoverStakingAccount,
  quitApp,
  onK2ConnectionError,
  getTaskSubmissions,
  fetchKPLList,
  getKPLBalance,
  transferKplToken,
  resetPin,
  recoverLostTokens,
  resetTasksCache,
  getKPLStakingAccountPubKey,
  verifyMessage,
  checkVipAccess,
};
