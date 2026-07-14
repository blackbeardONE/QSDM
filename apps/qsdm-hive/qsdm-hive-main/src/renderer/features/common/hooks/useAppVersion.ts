import { useQuery } from 'react-query';

import { QueryKeys, getVersion } from 'renderer/services';
import { formatHiveVersion } from 'utils';

export const useAppVersion = () => {
  const { data, ...query } = useQuery(QueryKeys.AppVersion, getVersion, {
    refetchInterval: Infinity,
  });
  const internalVersion =
    process.env.NODE_ENV === 'development'
      ? data?.packageVersion
      : data?.appVersion;
  const appVersion = formatHiveVersion(internalVersion);

  return { ...query, appVersion };
};
