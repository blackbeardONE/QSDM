import { verifyQsdmSkyFangWalletLinked } from 'main/services/qsdmSystemTasks';
import { QsdmSkyFangLinkStatusResponse } from 'models/api/qsdm';

export const getQsdmSkyFangLinkStatus =
  async (): Promise<QsdmSkyFangLinkStatusResponse> => {
    const checkedAt = new Date().toISOString();
    const gate = await verifyQsdmSkyFangWalletLinked();

    return {
      configured: Boolean(gate.sender),
      linked: gate.ok,
      address: gate.sender || undefined,
      account: gate.account,
      username: gate.username,
      player: gate.player,
      linkedAt: gate.linkedAt,
      site: gate.site,
      skyFangStakeCell: gate.skyFangStakeCell,
      inGameStakeCell: gate.inGameStakeCell,
      gameStakeCell: gate.gameStakeCell,
      totalGameStakeCell: gate.totalGameStakeCell,
      hiveStakeCell: gate.hiveStakeCell,
      totalStakeCell: gate.totalStakeCell,
      rewardRateCell: gate.rewardRateCell,
      rewardModel: gate.rewardModel,
      detail: gate.ok ? undefined : gate.detail,
      checkedAt,
    };
  };
