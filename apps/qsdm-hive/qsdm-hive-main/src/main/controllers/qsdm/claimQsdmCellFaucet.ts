import { Event } from 'electron';
import fs from 'fs';
import path from 'path';

import axios from 'axios';

import {
  buildConfiguredQsdmCoreApiUrl,
  QSDM_CORE_CONNECTION_MODE,
  QSDM_WALLET_ADDRESS,
} from 'config/qsdm';
import { assertQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import { selectQsdmWalletAddress } from 'main/services/qsdmWalletAddress';
import {
  QsdmCellFaucetClaimRequest,
  QsdmCellFaucetClaimResponse,
} from 'models/api/qsdm';

const tokenHeader = 'X-QSDM-Local-Faucet-Token';

const readEnv = (key: string) => process.env[key]?.trim() || '';

const uniquePaths = (values: string[]) =>
  Array.from(
    new Set(values.filter(Boolean).map((value) => path.resolve(value)))
  );

const findFirstExistingFile = (candidates: string[]) =>
  candidates.find((candidate) => {
    try {
      return fs.existsSync(candidate) && fs.statSync(candidate).isFile();
    } catch {
      return false;
    }
  }) || '';

const getCandidateRoots = () =>
  uniquePaths([
    readEnv('QSDM_WORKSPACE_ROOT'),
    readEnv('QSDM_REPO_ROOT'),
    process.cwd(),
    path.resolve(process.cwd(), '..', '..', '..'),
    path.resolve(__dirname, '..', '..', '..', '..', '..'),
  ]);

const getLocalFaucetToken = () => {
  const envToken = readEnv('QSDM_LOCAL_CELL_FAUCET_TOKEN');
  if (envToken) return envToken;

  const tokenPath = findFirstExistingFile(
    getCandidateRoots().flatMap((root) => [
      path.join(
        root,
        'QSDM',
        'source',
        '.cache',
        'local-validator',
        'run-v2',
        'qsdm-local-faucet.token'
      ),
      path.join(
        root,
        'source',
        '.cache',
        'local-validator',
        'run-v2',
        'qsdm-local-faucet.token'
      ),
    ])
  );

  if (!tokenPath) return '';
  return fs.readFileSync(tokenPath, 'utf-8').trim();
};

const getErrorMessage = (error: unknown) => {
  if (axios.isAxiosError(error)) {
    const responseData = error.response?.data as
      | { message?: string; error?: string }
      | undefined;
    return responseData?.message || responseData?.error || error.message;
  }
  if (error instanceof Error) return error.message;
  return String(error);
};

export const claimQsdmCellFaucet = async (
  _: Event,
  payload?: QsdmCellFaucetClaimRequest
): Promise<QsdmCellFaucetClaimResponse> => {
  if (QSDM_CORE_CONNECTION_MODE !== 'local') {
    throw new Error(
      'An operator-funded starter grant is available only when the local validator has a funded onboarding treasury.'
    );
  }

  await assertQsdmCanonicalChainSafety({ allowGatewayFallback: false });

  const address = selectQsdmWalletAddress({
    requestedAddress: payload?.address,
    signerAddress: getQsdmTaskActionSender(),
    configuredAddress: QSDM_WALLET_ADDRESS,
  });

  if (!address) {
    throw new Error('QSDM wallet address is not configured');
  }

  const token = getLocalFaucetToken();
  if (!token) {
    throw new Error(
      'The local validator has not enabled its operator-funded starter grant.'
    );
  }

  try {
    const response = await axios.post<QsdmCellFaucetClaimResponse>(
      buildConfiguredQsdmCoreApiUrl('/faucet/claim'),
      {
        address,
        amount: payload?.amount,
      },
      {
        headers: {
          [tokenHeader]: token,
        },
        timeout: 10000,
      }
    );
    return response.data;
  } catch (error) {
    throw new Error(`QSDM starter grant failed: ${getErrorMessage(error)}`);
  }
};
