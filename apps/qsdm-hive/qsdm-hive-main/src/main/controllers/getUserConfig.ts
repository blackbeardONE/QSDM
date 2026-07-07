import { SystemDbKeys } from 'config/systemDbKeys';
import { namespaceInstance } from 'main/node/helpers/Namespace';
import { getUserConfigResponse } from 'models/api';

const getUserConfig = async (): Promise<getUserConfigResponse> => {
  try {
    const userConfigStringified: string = await namespaceInstance.storeGet(
      SystemDbKeys.UserConfig
    );
    if (!userConfigStringified) {
      return {};
    }
    const userConfig = JSON.parse(
      userConfigStringified
    ) as getUserConfigResponse;
    return userConfig;
  } catch (err: any) {
    console.error('GET USER CONFIG', err);
    return {};
  }
};

export default getUserConfig;
