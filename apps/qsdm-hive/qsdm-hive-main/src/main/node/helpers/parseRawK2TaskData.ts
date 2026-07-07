import { RawTaskData, TaskData } from 'models';
import { normalizeQsdmTaskType } from 'utils';
import { PublicKey } from 'vendor/qsdm-chain/web3';

const toDisplayKey = (value?: unknown) => {
  if (!value) return '';
  if (value instanceof PublicKey) return value.toBase58();
  if (typeof value !== 'string') return String(value);
  try {
    return new PublicKey(value).toBase58();
  } catch {
    return value;
  }
};

export function parseRawK2TaskData({
  rawTaskData,
  hasError = false,
}: {
  rawTaskData: RawTaskData;
  isRunning?: boolean;
  hasError?: boolean;
}): TaskData {
  return {
    taskName: rawTaskData.task_name,
    taskManager: toDisplayKey(rawTaskData.task_manager),
    isWhitelisted: rawTaskData.is_allowlisted,
    isActive: rawTaskData.is_active,
    taskAuditProgram: rawTaskData.task_audit_program,
    stakePotAccount: toDisplayKey(rawTaskData.stake_pot_account),
    totalBountyAmount: rawTaskData.total_bounty_amount,
    bountyAmountPerRound: rawTaskData.bounty_amount_per_round,
    currentRound: rawTaskData.current_round,
    availableBalances: rawTaskData.available_balances,
    stakeList: rawTaskData.stake_list,
    startingSlot: rawTaskData.starting_slot,
    isRunning: rawTaskData.is_running ?? false,
    hasError,
    metadataCID: rawTaskData.task_metadata,
    minimumStakeAmount: rawTaskData.minimum_stake_amount,
    roundTime: rawTaskData.round_time,
    submissions: rawTaskData.submissions,
    distributionsAuditTrigger: rawTaskData.distributions_audit_trigger,
    submissionsAuditTrigger: rawTaskData.submissions_audit_trigger,
    isMigrated: rawTaskData.is_migrated,
    migratedTo: rawTaskData.migrated_to,
    distributionRewardsSubmission: rawTaskData.distribution_rewards_submission,
    taskType: normalizeQsdmTaskType({
      taskType: rawTaskData?.task_type,
      tokenType: rawTaskData?.token_type,
    }),
    tokenType: toDisplayKey(rawTaskData?.token_type),
  };
}
