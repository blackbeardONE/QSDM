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
      detail: gate.ok ? undefined : gate.detail,
      checkedAt,
    };
  };
