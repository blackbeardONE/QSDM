export const downloadAppUpdate = async () => {
  return window.main.downloadAppUpdate();
};

export const getHiveVersionPolicy = async (
  options: { forceRefresh?: boolean } = {}
) => {
  return window.main.getHiveVersionPolicy(options);
};

export const quitApp = async () => {
  return window.main.quitApp();
};

export const checkAppUpdate = async () => {
  return window.main.checkAppUpdate();
};
