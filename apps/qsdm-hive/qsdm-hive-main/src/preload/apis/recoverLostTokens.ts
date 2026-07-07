import config from 'config';
import sendMessage from 'preload/sendMessage';

export interface RecoverLostTokensResult {
  status: 'skipped' | 'completed';
  message: string;
  recoveredRewards: number;
  recoveredStakes: number;
  actions?: string[];
  finalBalance?: number;
}

export default (): Promise<RecoverLostTokensResult> =>
  sendMessage(config.endpoints.RECOVER_LOST_TOKENS, {});
