import { app } from 'electron';

export const getVersion = async (): Promise<{
  appVersion: string;
  packageVersion: string;
}> => {
  const appVersion = app.getVersion();
  return {
    appVersion,
    packageVersion: appVersion,
  };
};
