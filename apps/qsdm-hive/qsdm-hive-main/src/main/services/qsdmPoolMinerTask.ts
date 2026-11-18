// QSDM Pool Miner: the one-button way to mine with the QSDM pool (PULSE).
//
// It is the same packaged miner and GPU enrollment as QSDM Miner. Starting it
// decides, in plain steps, how the miner runs this time:
//   - before the PULSE rule is active on the network, it mines solo and says
//     when pool payouts start;
//   - once the rule is active, a wallet that is not yet in the pool is asked
//     once to join (one signed transaction), then it mines in the pool with
//     the default pool address, the built-in backup and the solo fallback.
// The miner falls back to solo by itself while the pool is unreachable and
// returns to the pool when it answers again.
import {
  QSDM_PULSE_ACTIVATION_DATE_TEXT,
  QSDM_PULSE_ACTIVATION_HEIGHT,
} from 'config/qsdmSystemTasks';
import { QsdmPulsePoolStatus } from 'models/api/qsdm';

export type QsdmPoolMinerPulseMode = 'force-on' | 'off';

export type QsdmPoolMinerStartPlan = {
  pulse: QsdmPoolMinerPulseMode;
  message: string;
  joined?: boolean;
};

export type QsdmPoolMinerJoinPrompt = {
  feeBps?: number;
  networkFeeCapBps?: number;
};

export const describeQsdmPulseActivation = (activationHeight?: number) =>
  `Pool payouts start at block ${(
    activationHeight || QSDM_PULSE_ACTIVATION_HEIGHT
  ).toLocaleString(
    'en-US'
  )} (${QSDM_PULSE_ACTIVATION_DATE_TEXT}). Until then this task mines solo.`;

export const formatQsdmPoolFee = (bps?: number) =>
  typeof bps === 'number' ? `${(bps / 100).toLocaleString('en-US')}%` : '1%';

// The text of the one join question, in plain words.
export const buildQsdmPoolJoinDetail = ({
  feeBps,
  networkFeeCapBps,
}: QsdmPoolMinerJoinPrompt) =>
  [
    'Your mining rewards collect in an escrow that nobody can spend, not even the pool.',
    "The network pays you from it every block, by your share of the pool's work.",
    `Pool fee: ${formatQsdmPoolFee(feeBps)}${
      typeof networkFeeCapBps === 'number'
        ? ` (the network caps pool fees at ${formatQsdmPoolFee(
            networkFeeCapBps
          )})`
        : ''
    }.`,
    'You can leave any time. If the pool ever stops paying, you can take your rewards back after about a day.',
  ].join('\n\n');

export const planQsdmPoolMinerStart = async ({
  getStatus,
  confirmJoin,
  join,
}: {
  getStatus: () => Promise<QsdmPulsePoolStatus>;
  confirmJoin: (prompt: QsdmPoolMinerJoinPrompt) => Promise<boolean>;
  join: () => Promise<unknown>;
}): Promise<QsdmPoolMinerStartPlan> => {
  const status = await getStatus();

  if (!status.ruleActive) {
    return {
      pulse: 'off',
      message: describeQsdmPulseActivation(status.activationHeight),
    };
  }

  if (
    status.member &&
    status.memberPool &&
    status.poolId &&
    status.memberPool !== status.poolId
  ) {
    throw new Error(
      'This wallet is in another PULSE pool. Leave that pool first, then start QSDM Pool Miner.'
    );
  }

  if (status.member) {
    return {
      pulse: 'force-on',
      message: 'Mining in the QSDM pool.',
    };
  }

  if (!status.poolId) {
    return {
      pulse: 'off',
      message:
        'The QSDM pool is not answering, so this wallet cannot join it right now. Mining solo; start again later to join the pool.',
    };
  }

  const confirmed = await confirmJoin({
    feeBps: status.feeBps,
    networkFeeCapBps: status.networkFeeCapBps,
  });
  if (!confirmed) {
    throw new Error(
      'Joining the QSDM pool was cancelled. Nothing was changed.'
    );
  }
  await join();
  return {
    pulse: 'force-on',
    message: 'Joined the QSDM pool. Mining in the pool.',
    joined: true,
  };
};
