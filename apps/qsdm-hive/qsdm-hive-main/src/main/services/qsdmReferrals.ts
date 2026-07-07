import axios from 'axios';
import { randomBytes } from 'crypto';

import { buildQsdmCoreApiUrl } from 'config/qsdm';
import {
  QsdmReferralClaimRequest,
  QsdmReferralClaimResponse,
  QsdmReferralRegisterResponse,
  QsdmReferralRewardPoolStatus,
  QsdmReferralRegistrationRequest,
  QsdmReferralStatusResponse,
} from 'models/api/qsdm';

import {
  getQsdmTaskActionSender,
} from './qsdmTaskActionSigner';
import { assertQsdmCanonicalChainSafety } from './qsdmCanonicalChain';
import { signQsdmMessageWithCli } from './qsdmTaskActions';

type UnsignedReferralEnvelope = {
  id: string;
  referrer: string;
  referred: string;
  referral_code: string;
  install_id?: string;
  timestamp: string;
  signature: '';
};

type SignedReferralEnvelope = Omit<UnsignedReferralEnvelope, 'signature'> & {
  signature: string;
  public_key: string;
};

const SAFE_QSDM_ADDRESS = /^[0-9a-fA-F]{64}$/;
const SAFE_REFERRAL_CODE = /^[0-9A-Za-z_-]{6,64}$/;

const makeReferralRegistrationId = () =>
  `hive_ref_${Date.now()}_${randomBytes(8).toString('hex')}`;

const normalizeAddress = (value: string, label: string) => {
  const normalized = value.trim().toLowerCase();
  if (!SAFE_QSDM_ADDRESS.test(normalized)) {
    throw new Error(`${label} must be a 64-character QSDM wallet address`);
  }
  return normalized;
};

const normalizeReferralCode = (value: string) => {
  const normalized = value.trim().toUpperCase();
  if (!SAFE_REFERRAL_CODE.test(normalized)) {
    throw new Error('Referral code has an invalid format');
  }
  return normalized;
};

const getErrorMessage = (error: unknown) => {
  if (axios.isAxiosError(error)) {
    const responseData = error.response?.data as
      | { error?: string; message?: string }
      | undefined;
    return responseData?.message || responseData?.error || error.message;
  }
  if (error instanceof Error) return error.message;
  return String(error);
};

const getCurrentQsdmSignerAddress = () => {
  const sender = getQsdmTaskActionSender();
  if (!sender) {
    throw new Error(
      'QSDM signer wallet is not configured. Import or activate a QSDM wallet first.'
    );
  }
  return normalizeAddress(sender, 'QSDM signer wallet');
};

const buildUnsignedReferralEnvelope = ({
  referrer,
  referred,
  referralCode,
  installId,
}: {
  referrer: string;
  referred: string;
  referralCode: string;
  installId?: string;
}): UnsignedReferralEnvelope => {
  const envelope: UnsignedReferralEnvelope = {
    id: makeReferralRegistrationId(),
    referrer,
    referred,
    referral_code: referralCode,
    timestamp: new Date().toISOString(),
    signature: '',
  };

  if (installId?.trim()) {
    envelope.install_id = installId.trim();
  }

  return envelope;
};

export const getQsdmReferralStatus = async (
  referred: string
): Promise<QsdmReferralStatusResponse> => {
  const url = new URL(buildQsdmCoreApiUrl('/referrals/status'));
  url.searchParams.set('referred', normalizeAddress(referred, 'referred'));
  const response = await axios.get<QsdmReferralStatusResponse>(url.toString(), {
    timeout: 10000,
  });
  return response.data;
};

export const getQsdmReferralRewardPoolStatus = async () => {
  const response = await axios.get<QsdmReferralRewardPoolStatus>(
    buildQsdmCoreApiUrl('/referrals/reward-pool'),
    { timeout: 10000 }
  );
  return response.data;
};

export const registerQsdmReferral = async ({
  referrer,
  referralCode,
  installId,
}: QsdmReferralRegistrationRequest): Promise<QsdmReferralRegisterResponse> => {
  await assertQsdmCanonicalChainSafety();
  const referred = getCurrentQsdmSignerAddress();
  const normalizedReferrer = normalizeAddress(referrer, 'referrer');
  const normalizedReferralCode = normalizeReferralCode(referralCode);

  if (normalizedReferrer === referred) {
    throw new Error('Self-referrals are not allowed.');
  }

  const unsignedEnvelope = buildUnsignedReferralEnvelope({
    referrer: normalizedReferrer,
    referred,
    referralCode: normalizedReferralCode,
    installId,
  });
  const canonicalEnvelope = JSON.stringify(unsignedEnvelope);
  const signed = await signQsdmMessageWithCli(canonicalEnvelope);

  if (signed.address.toLowerCase() !== referred) {
    throw new Error(
      `Active signer ${signed.address} does not match referred wallet ${referred}`
    );
  }

  const signedEnvelope: SignedReferralEnvelope = {
    ...unsignedEnvelope,
    signature: signed.signature,
    public_key: signed.public_key,
  };

  try {
    const response = await axios.post<QsdmReferralRegisterResponse>(
      buildQsdmCoreApiUrl('/referrals/register-signed'),
      signedEnvelope,
      { timeout: 10000 }
    );
    return response.data;
  } catch (error) {
    throw new Error(
      `QSDM referral registration failed: ${getErrorMessage(error)}`
    );
  }
};

export const claimQsdmReferralReward = async (
  payload: QsdmReferralClaimRequest = {}
): Promise<QsdmReferralClaimResponse> => {
  await assertQsdmCanonicalChainSafety();
  const referred = normalizeAddress(
    payload.referred || getCurrentQsdmSignerAddress(),
    'referred'
  );
  let referrer = payload.referrer
    ? normalizeAddress(payload.referrer, 'referrer')
    : '';

  if (!referrer) {
    const status = await getQsdmReferralStatus(referred);
    referrer = status.registration?.referrer || '';
  }

  if (!referrer) {
    throw new Error('This wallet has no signed referral registration yet.');
  }

  try {
    const response = await axios.post<QsdmReferralClaimResponse>(
      buildQsdmCoreApiUrl('/referrals/claim'),
      { referrer, referred },
      { timeout: 10000 }
    );
    return response.data;
  } catch (error) {
    throw new Error(`QSDM referral claim failed: ${getErrorMessage(error)}`);
  }
};
