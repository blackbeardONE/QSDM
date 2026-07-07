import { OpenBrowserWindowParam } from 'models/api';

import { openValidatedExternalUrl } from '../security/externalNavigation';

const openBrowserWindow = async (_: Event, { URL }: OpenBrowserWindowParam) => {
  await openValidatedExternalUrl(URL);
};

export default openBrowserWindow;
