import axios from 'axios';
import { buildQsdmCoreApiUrl, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { getMyTaskSubmissionRoundInfo as getMyTaskSubmissionRoundInfoK2 } from 'vendor/qsdm-chain/taskNode';
import { getStakingAccountKeypair } from 'main/node/helpers';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import sdk from 'main/services/sdk';
import { QsdmTaskResponse } from 'models/api/qsdm';

export type GetMyTaskSubmissionInfoParams = {
  taskAccountPubKey: string;
  round: number;
};

export type SubmissionInfoType = Awaited<
  ReturnType<typeof getMyTaskSubmissionRoundInfoK2>
>;

export async function getMyTaskSubmissionRoundInfo(
  _: Event,
  { taskAccountPubKey, round }: GetMyTaskSubmissionInfoParams
): Promise<SubmissionInfoType | null> {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    const sender = getQsdmTaskActionSender();
    if (!sender) {
      return null;
    }

    try {
      const response = await axios.get<QsdmTaskResponse>(
        buildQsdmCoreApiUrl(`/tasks/${encodeURIComponent(taskAccountPubKey)}`),
        { timeout: 10000 }
      );
      return (
        (response.data.task?.submissions?.[String(round)]?.[sender] as
          | SubmissionInfoType
          | undefined) || null
      );
    } catch (error) {
      console.log('Error in native getMyTaskSubmissionRoundInfo', error);
      return null;
    }
  }

  try {
    const stakingAccKeypair = await getStakingAccountKeypair();
    const stakingPubkey = stakingAccKeypair.publicKey.toBase58();
    const taskStakeInfo = await getMyTaskSubmissionRoundInfoK2(
      sdk.k2Connection,
      taskAccountPubKey,
      stakingPubkey,
      round,
      'CELL'
    );

    return taskStakeInfo;
  } catch (error) {
    console.log('Error in getMyTaskSubmissionRoundInfo', error);
    return null;
  }
}
