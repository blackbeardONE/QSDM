import axios from 'axios';
import { dialog, shell } from 'electron';

import { RendererEndpoints } from 'config/endpoints';
import { DeepLinkRoute } from 'renderer/types/routes';
import { sendEventAllWindows } from 'utils/sendEventAllWindows';

import { storeTaskVariable } from './controllers';
import { signQsdmMessageWithCli } from './services/qsdmTaskActions';
import { signMessageWithSystemWallet } from './util';

const allowedAppRoutes = Object.values(DeepLinkRoute);
function checkIfRouteIsValid(route: string): boolean {
  return allowedAppRoutes.includes(route);
}

interface SkyFangLinkChallenge {
  code?: string;
  message?: string;
  account?: string;
  player?: string;
  expires_at?: string;
}

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
  if (!value) {
    throw new Error(`${label} is required`);
  }
  const url = new URL(value);
  const localHttp = url.protocol === 'http:' && LOCAL_HOSTS.has(url.hostname);
  if (url.protocol !== 'https:' && !localHttp) {
    throw new Error(`${label} must use https, except localhost development URLs`);
  }
  return url;
};

const sameOrigin = (left: URL, right: URL) =>
  left.protocol === right.protocol && left.host === right.host;

const appendStatus = (
  rawURL: string | null,
  status: 'success' | 'cancelled' | 'error',
  details: Record<string, string | undefined> = {}
) => {
  if (!rawURL) return '';
  const url = new URL(rawURL);
  url.searchParams.set('qsdm_link', status);
  Object.entries(details).forEach(([key, value]) => {
    if (value) url.searchParams.set(key, value);
  });
  return url.toString();
};

const openReturnUrl = async (
  rawURL: string | null,
  status: 'success' | 'cancelled' | 'error',
  details: Record<string, string | undefined> = {}
) => {
  if (!rawURL) return;
  await shell.openExternal(appendStatus(rawURL, status, details));
};

const validateSkyFangLinkUrls = ({
  challengeURL,
  submitURL,
  returnURL,
}: {
  challengeURL: URL;
  submitURL: URL;
  returnURL: URL | null;
}) => {
  if (!sameOrigin(challengeURL, submitURL)) {
    throw new Error('Sky Fang submit_url must use the same origin as challenge_url');
  }
  if (returnURL && !sameOrigin(challengeURL, returnURL)) {
    throw new Error('Sky Fang return_url must use the same origin as challenge_url');
  }
};

const handleSkyFangLink = async (searchParams: URLSearchParams) => {
  const returnURLRaw = searchParams.get('return_url');

  try {
    const challengeURL = parseHttpUrl(
      searchParams.get('challenge_url') || '',
      'challenge_url'
    );
    const submitURL = parseHttpUrl(
      searchParams.get('submit_url') || '',
      'submit_url'
    );
    const returnURL = returnURLRaw
      ? parseHttpUrl(returnURLRaw, 'return_url')
      : null;

    validateSkyFangLinkUrls({ challengeURL, submitURL, returnURL });

    const challengeResponse = await axios.get<SkyFangLinkChallenge>(
      challengeURL.toString(),
      { timeout: 15000 }
    );
    const challenge = challengeResponse.data || {};
    const message = challenge.message || '';
    const code =
      searchParams.get('code') || challenge.code || challengeURL.searchParams.get('code') || '';

    if (!message.startsWith('QSDM-LINK:')) {
      throw new Error('Sky Fang challenge is not a QSDM-LINK message');
    }
    if (!code) {
      throw new Error('Sky Fang link code was not provided');
    }
    const messageCode = message.split(':')[1];
    if (messageCode && messageCode !== code) {
      throw new Error('Sky Fang link code does not match the challenge');
    }

    const detailLines = [
      `Site: ${challengeURL.origin}`,
      `Code: ${code}`,
      challenge.account ? `Account: ${challenge.account}` : '',
      challenge.player ? `Player: ${challenge.player}` : '',
      challenge.expires_at ? `Expires: ${challenge.expires_at}` : '',
      '',
      `Message: ${message}`,
    ].filter(Boolean);

    const confirmation = await dialog.showMessageBox({
      title: 'Link Sky Fang account',
      message: 'Sky Fang is requesting ownership proof for your QSDM wallet.',
      detail: detailLines.join('\n'),
      type: 'question',
      buttons: ['Link Wallet', 'Cancel'],
      defaultId: 1,
      cancelId: 1,
    });

    if (confirmation.response !== 0) {
      await openReturnUrl(returnURLRaw, 'cancelled');
      return;
    }

    const signed = await signQsdmMessageWithCli(message);
    const submitResponse = await axios.post(
      submitURL.toString(),
      {
        code,
        public_key: signed.public_key,
        signature: signed.signature,
      },
      { timeout: 15000 }
    );

    const linkedAddress =
      (submitResponse.data as { address?: string })?.address || signed.address;

    await dialog.showMessageBox({
      title: 'Sky Fang account linked',
      message: 'Your QSDM wallet was linked to Sky Fang.',
      detail: `Address: ${linkedAddress}`,
      type: 'info',
      buttons: ['OK'],
    });
    await openReturnUrl(returnURLRaw, 'success', { address: linkedAddress });
  } catch (error) {
    const message = getErrorMessage(error);
    await dialog.showMessageBox({
      title: 'Sky Fang wallet link failed',
      message,
      type: 'error',
      buttons: ['OK'],
    });
    try {
      await openReturnUrl(returnURLRaw, 'error', { message });
    } catch {
      // The return URL itself may be what failed validation.
    }
  }
};

export const handleDeepLinks = async (argv: string[]) => {
  console.log('- DEEP LINK -', { argv });
  for (const arg of argv) {
    if (arg.startsWith('qsdm-hive://')) {
      const deepLink = arg;
      const url = new URL(deepLink);
      const searchParams = new URLSearchParams(url.search);

      const action = url.host;
      console.log({ deepLink });

      switch (action) {
        case 'open': {
          const route = searchParams.get('route') || '';
          const isValidRoute = route && checkIfRouteIsValid(route);
          console.log({ route, isValidRoute });

          if (isValidRoute) {
            sendEventAllWindows(RendererEndpoints.NAVIDATE_TO_ROUTE, route);
          }
          break;
        }
        case 'skyfang-link': {
          void handleSkyFangLink(searchParams);
          break;
        }
        case 'sign-message': {
          const data = searchParams.get('data') || '';
          const callbackURL = searchParams.get('callback');
          console.log({ action, data, callbackURL });
          dialog
            .showMessageBox({
              title: 'Requesting to sign message',
              message: `Action: ${action}`,
              type: 'info',
              buttons: ['Sign', 'Cancel'],
            })
            .then(async (response) => {
              const buttonIndex = response.response;
              let signedMessage;
              switch (buttonIndex) {
                case 0:
                  console.log('SIGNED BUTTON CALLED');
                  signedMessage = await signMessageWithSystemWallet(data);
                  if (callbackURL) {
                    const callbackURLWithParams = `${callbackURL}?signedMessage=${signedMessage}`;
                    console.log('callbackURLWithParams', callbackURLWithParams);
                    shell.openExternal(callbackURLWithParams);
                  }
                  break;
                default:
                  console.log('CANCEL BUTTON CALLED');
                  break;
              }
            });
          break;
        }
        case 'store-variable': {
          const variableName = searchParams.get('variableName') || '';
          const variableValue = searchParams.get('variableValue') || '';

          dialog
            .showMessageBox({
              title: `Requesting to store variable ${variableName}`,
              message: `Action: ${action}`,
              type: 'info',
              buttons: ['Store', 'Cancel'],
            })
            .then(async (response) => {
              const buttonIndex = response.response;
              switch (buttonIndex) {
                case 0:
                  storeTaskVariable({} as Event, {
                    label: variableName,
                    value: variableValue,
                  });
                  break;
                default:
                  console.log('CANCEL BUTTON CALLED');
                  break;
              }
            });
          break;
        }
        default: {
          break;
        }
      }
    }
  }
};
