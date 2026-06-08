import { FetchAndSaveuPnPBinaryReturnValue, UPnPBinaryStatus } from 'models';

export const fetchAndSaveUPnPBinary =
  (): Promise<FetchAndSaveuPnPBinaryReturnValue> => {
    return window.main.fetchAndSaveUPnPBinary();
  };

export const checkUPnPbinary = async (): Promise<UPnPBinaryStatus> => {
  const status = (await window.main.checkUPnPBinary()) as
    | boolean
    | UPnPBinaryStatus;

  if (typeof status === 'boolean') {
    return {
      exists: status,
      path: '',
      downloadConfigured: false,
    };
  }

  return status;
};
