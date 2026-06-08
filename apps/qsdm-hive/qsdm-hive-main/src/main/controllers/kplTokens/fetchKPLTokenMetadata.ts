import {
  ErrorType,
  FetchKPLTokenMetadataParams,
  FetchKPLTokenMetadataResponse,
} from 'models';
import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { throwDetailedError } from 'utils';

const getFetch = async (): Promise<typeof fetch> => {
  if (globalThis.fetch) {
    return globalThis.fetch.bind(globalThis);
  }
  const mod = await new Function('return import("node-fetch")')();
  return mod.default;
};

export const fetchKPLTokenMetadata = async (
  _: Event,
  { mintAddress }: FetchKPLTokenMetadataParams
): Promise<FetchKPLTokenMetadataResponse> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    return {
      chainId: 0,
      address: mintAddress,
      decimals: 0,
      name: 'Unsupported token',
      symbol: 'Unsupported',
      description: 'QSDM Hive currently supports CELL only.',
      logoURI: 'Unknown',
      tags: [],
    };
  }

  const kplMetadataUrl = `https://api.qsdm.tech/token-metadata/retrieveOne?tokenAddress=${mintAddress}`;
  try {
    const fetchImpl = await getFetch();
    const response = await fetchImpl(kplMetadataUrl);
    if (!response.ok) {
      // Return fallback data if metadata is not found
      return {
        chainId: 0,
        address: mintAddress,
        decimals: 0,
        name: 'Unknown',
        symbol: 'Unknown',
        description: 'Unknown',
        logoURI: 'Unknown',
        tags: [],
      };
    }
    const jsonData = (await response.json()) as FetchKPLTokenMetadataResponse;
    return jsonData;
  } catch (error) {
    console.error(error);
    return throwDetailedError({
      detailed: error as string,
      type: ErrorType.GENERIC,
    });
  }
};
