import axios from 'axios';

import {
  QsdmSkyFangLinkCodeRequest,
  QsdmSkyFangLinkResponse,
} from 'models/api/qsdm';

import { signQsdmMessageWithCli } from './qsdmTaskActions';

type SkyFangLinkChallenge = {
  code?: string;
  message?: string;
  account?: string;
  username?: string;
  player?: string;
  expires_at?: string;
  site?: string;
};

type SkyFangLinkSubmitResponse = {
  ok?: boolean;
  address?: string;
  account?: string;
  username?: string;
  player?: string;
  linked_at?: string;
  site?: string;
};

const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '::1']);

const getErrorMessage = (error: unknown) => {
  if (axios.isAxiosError(error)) {
    const responseError = error.response?.data as
      | { error?: string; message?: string }
      | undefined;
    return (
      responseError?.error ||
      responseError?.message ||
      error.message ||
      'request failed'
    );
  }
  if (error instanceof Error) return error.message;
  return String(error);
};

const parseHttpUrl = (value: string, label: string) => {
  if (!value.trim()) {
    throw new Error(`${label} is required`);
  }
  const url = new URL(value.trim());
  const localHttp = url.protocol === 'http:' && LOCAL_HOSTS.has(url.hostname);
  if (url.protocol !== 'https:' && !localHttp) {
    throw new Error(`${label} must use https, except localhost development URLs`);
  }
  return url;
};

export const getSkyFangBaseUrl = (baseUrl?: string) => {
  const raw =
    baseUrl?.trim() ||
    process.env.QSDM_SKYFANG_BASE_URL?.trim() ||
    'https://skyfang.xyz';
  const url = parseHttpUrl(raw.replace(/\/+$/, ''), 'Sky Fang base URL');
  return url.origin;
};

export const getSkyFangDashboardUrl = (baseUrl?: string) =>
  `${getSkyFangBaseUrl(baseUrl)}/login?next=/dashboard/qsdm`;

const normalizeCode = (rawCode?: string) => {
  const code = (rawCode || '').trim();
  if (!code) {
    throw new Error('Sky Fang link code is required');
  }
  if (!/^[0-9A-Za-z_-]{4,64}$/.test(code)) {
    throw new Error('Sky Fang link code contains unsupported characters');
  }
  return code;
};

const validateChallenge = (challenge: SkyFangLinkChallenge, code: string) => {
  const message = challenge.message || '';
  if (!message.startsWith('QSDM-LINK:')) {
    throw new Error('Sky Fang challenge is not a QSDM-LINK message');
  }

  const messageCode = message.split(':')[1];
  if (!messageCode || messageCode !== code) {
    throw new Error('Sky Fang link code does not match the challenge');
  }

  return message;
};

export const linkSkyFangWalletByCode = async ({
  code: rawCode,
  baseUrl,
}: QsdmSkyFangLinkCodeRequest): Promise<QsdmSkyFangLinkResponse> => {
  const checkedAt = new Date().toISOString();
  const code = normalizeCode(rawCode);
  const base = getSkyFangBaseUrl(baseUrl);
  const challengeUrl = `${base}/api/qsdm/link-challenge?code=${encodeURIComponent(
    code
  )}`;
  const submitUrl = `${base}/api/qsdm/link`;

  try {
    const challengeResponse = await axios.get<SkyFangLinkChallenge>(
      challengeUrl,
      { timeout: 15000 }
    );
    const challenge = challengeResponse.data || {};
    const message = validateChallenge(challenge, code);
    const signed = await signQsdmMessageWithCli(message);

    const submitResponse = await axios.post<SkyFangLinkSubmitResponse>(
      submitUrl,
      {
        code,
        public_key: signed.public_key,
        signature: signed.signature,
      },
      { timeout: 15000 }
    );
    const submitted = submitResponse.data || {};
    const address = (submitted.address || signed.address).toLowerCase();

    return {
      ok: submitted.ok !== false,
      code,
      address,
      publicKey: signed.public_key,
      account: submitted.account || challenge.account,
      username: submitted.username || challenge.username,
      player: submitted.player || challenge.player,
      linkedAt: submitted.linked_at,
      site: submitted.site || challenge.site || base,
      checkedAt,
    };
  } catch (error) {
    throw new Error(`Sky Fang wallet link failed: ${getErrorMessage(error)}`);
  }
};
