import {
  Button,
  ButtonSize,
  ButtonVariant,
  CheckSuccessLine,
  CopyLine,
  Icon,
} from 'vendor/qsdm-styleguide';
import React, { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from 'react-query';

import { QsdmReferralStatusResponse } from 'models/api/qsdm';
import { LoadingSpinner, Tooltip } from 'renderer/components/ui';
import { useClipboard } from 'renderer/features/common';
import {
  QueryKeys,
  claimQsdmReferralReward,
  getQsdmReferralStatus,
  getQsdmReferralRewardPoolStatus,
  getReferralCode,
  registerQsdmReferral,
} from 'renderer/services';

import { useMainAccount, useUserAppConfig } from '../../../hooks';

type ReferralLinkParams = {
  referrer: string;
  referralCode: string;
};

const SAFE_QSDM_ADDRESS = /^[0-9a-fA-F]{64}$/;

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

const compactAddress = (address?: string) =>
  address && address.length > 16
    ? `${address.slice(0, 8)}...${address.slice(-8)}`
    : address || '-';

const parseReferralParamsFromSearch = (search: string) => {
  if (!search) return null;
  const params = new URLSearchParams(search.replace(/^[?#]/, ''));
  const referrer = params.get('referrer')?.trim() || '';
  const referralCode = params.get('refCode')?.trim() || '';

  if (!referrer || !referralCode) {
    return null;
  }

  return { referrer, referralCode };
};

const getReferralParamsFromLocation = (): ReferralLinkParams | null => {
  const direct = parseReferralParamsFromSearch(window.location.search);
  if (direct) return direct;

  const hash = window.location.hash || '';
  const queryIndex = hash.indexOf('?');
  if (queryIndex === -1) return null;
  return parseReferralParamsFromSearch(hash.slice(queryIndex));
};

const getReferralStatusLabel = (
  status?: QsdmReferralStatusResponse,
  claimsEnabled?: boolean
) => {
  if (!status) return 'not checked';
  if (!status.registered) return 'not registered';
  if (status.claimed) return 'reward released';
  if (status.claimable && claimsEnabled) return 'ready to release';
  if (status.qualified) return 'qualified, payouts locked';
  return 'registered, waiting for activity';
};

export function Referral() {
  const queryClient = useQueryClient();
  const { data: mainAccountPubKey = '' } = useMainAccount();
  const initialReferralParams = useMemo(getReferralParamsFromLocation, []);
  const [referrerInput, setReferrerInput] = useState(
    initialReferralParams?.referrer || ''
  );
  const [referralCodeInput, setReferralCodeInput] = useState(
    initialReferralParams?.referralCode || ''
  );
  const [manualStatus, setManualStatus] = useState('');

  const { data: referralCode = '' } = useQuery(
    [QueryKeys.ReferralCode, mainAccountPubKey],
    () => getReferralCode(mainAccountPubKey),
    { enabled: Boolean(mainAccountPubKey) }
  );
  const {
    data: rewardPoolStatus,
    isError: rewardPoolStatusError,
    refetch: refetchRewardPool,
  } = useQuery(
    [QueryKeys.ReferralRewardPool],
    getQsdmReferralRewardPoolStatus,
    {
    refetchInterval: 30000,
    retry: false,
    }
  );
  const {
    data: referralStatus,
    isError: referralStatusError,
    refetch: refetchReferralStatus,
  } = useQuery(
    [QueryKeys.ReferralStatus, mainAccountPubKey],
    () => getQsdmReferralStatus(mainAccountPubKey),
    {
      enabled: Boolean(mainAccountPubKey && SAFE_QSDM_ADDRESS.test(mainAccountPubKey)),
      refetchInterval: 30000,
      retry: false,
    }
  );

  const { userConfig, handleSaveUserAppConfig } = useUserAppConfig({});
  const { copyToClipboard: copyLink, copied: linkCopied } = useClipboard();
  const poolIsFunded = Boolean(rewardPoolStatus?.funded);
  const poolIsClaimable = Boolean(rewardPoolStatus?.claimable);
  const claimsEnabled = Boolean(rewardPoolStatus?.claims_enabled);
  const rewardPerReferral =
    rewardPoolStatus?.reward_per_qualified_referral || 0;
  const poolBalance = rewardPoolStatus?.balance || 0;
  const referralLink = `https://qsdm.tech?referrer=${mainAccountPubKey}&refCode=${referralCode}`;
  const poolStatusText = rewardPoolStatusError
    ? 'Core status unavailable'
    : poolIsClaimable
    ? 'claimable'
    : poolIsFunded
    ? 'funded, claims locked'
    : 'pending funding';
  const referralStatusLabel = getReferralStatusLabel(
    referralStatus,
    claimsEnabled
  );
  const registerDisabled =
    !referrerInput.trim() ||
    !referralCodeInput.trim() ||
    referrerInput.trim().toLowerCase() === mainAccountPubKey.toLowerCase();
  const canClaimReferral =
    Boolean(referralStatus?.claimable) &&
    !referralStatus?.claimed &&
    claimsEnabled;

  const registerReferralMutation = useMutation(registerQsdmReferral, {
    onSuccess: async (response) => {
      setManualStatus(response.message || 'Referral registered.');
      await queryClient.invalidateQueries([
        QueryKeys.ReferralStatus,
        mainAccountPubKey,
      ]);
      await queryClient.invalidateQueries([QueryKeys.ReferralRewardPool]);
      await refetchReferralStatus();
      await refetchRewardPool();
    },
    onError: (error) => {
      setManualStatus(getErrorMessage(error));
    },
  });

  const claimReferralMutation = useMutation(claimQsdmReferralReward, {
    onSuccess: async (response) => {
      setManualStatus(response.message || 'Referral reward released.');
      await queryClient.invalidateQueries([
        QueryKeys.ReferralStatus,
        mainAccountPubKey,
      ]);
      await queryClient.invalidateQueries([QueryKeys.ReferralRewardPool]);
      await refetchReferralStatus();
      await refetchRewardPool();
    },
    onError: (error) => {
      setManualStatus(getErrorMessage(error));
    },
  });

  useEffect(() => {
    if (initialReferralParams) {
      setManualStatus('Referral link detected. Register it to bind this wallet.');
    }
  }, [initialReferralParams]);

  const copyReferralLink = () => {
    const referralParagraph = `I just joined QSDM Hive. Use my referral link to install Hive and join the QSDM network.

Referral tracking is active; CELL referral rewards are paid by the QSDM Referral Reward Pool after funding and eligibility checks are enabled: ${referralLink}`;
    copyLink(referralParagraph);
    handleSaveUserAppConfig({ settings: { hasCopiedReferralCode: true } });
  };

  const registerReferral = () => {
    registerReferralMutation.mutate({
      referrer: referrerInput.trim(),
      referralCode: referralCodeInput.trim(),
    });
  };

  const claimReferral = () => {
    claimReferralMutation.mutate({});
  };

  const shouldSeeTheNewTag = userConfig?.hasCopiedReferralCode === false;

  const tooltipContent = linkCopied
    ? 'Copied!'
    : 'Copy your referral link to share QSDM Hive.';

  return (
    <div className="font-light text-white">
      <p className="mb-2">
        Share your referral link to help new users install QSDM Hive and join
        the CELL network.
      </p>
      <p className="mb-3">
        Referral tracking is active. CELL rewards are released only after the
        referred wallet signs its registration, the reward pool is funded, and
        the referred node meets the activity rule.
      </p>

      <div className="px-4 py-3 mb-4 text-sm rounded-md bg-finnieBlue-light-tertiary">
        Reward source: QSDM Referral Reward Pool. Current status:{' '}
        {poolStatusText}
        {rewardPoolStatus ? (
          <>
            {' '}
            · Pool {poolBalance.toLocaleString()} CELL · Reward{' '}
            {rewardPerReferral.toLocaleString()} CELL per qualified referral
            <span className="block mt-1 text-xs opacity-80">
              Ledger:{' '}
              {rewardPoolStatus.ledger_configured ? 'configured' : 'not configured'}{' '}
              · Registered {rewardPoolStatus.registrations || 0} · Qualified{' '}
              {rewardPoolStatus.qualified || 0} · Claimed{' '}
              {rewardPoolStatus.claimed || 0} · Pending{' '}
              {rewardPoolStatus.pending_claims || 0}
            </span>
          </>
        ) : null}
        {rewardPoolStatus?.message ? (
          <span className="block mt-1 text-xs opacity-80">
            {rewardPoolStatus.message}
          </span>
        ) : null}
      </div>

      <div className="flex items-center mt-5 gap-6">
        {referralCode ? (
          <>
            <Tooltip tooltipContent={tooltipContent}>
              <Button
                variant={ButtonVariant.Secondary}
                size={ButtonSize.SM}
                buttonClassesOverrides="!transition-all !duration-300 ease-in-out focus:text-white"
                label="Copy My Referral Link"
                labelClassesOverrides="mx-auto focus:text-white"
                data-testid="copy-referrals-button"
                id="copy-referrals-button"
                onClick={copyReferralLink}
                iconLeft={
                  <Icon
                    onClick={copyReferralLink}
                    source={linkCopied ? CheckSuccessLine : CopyLine}
                    className={`text-blue cursor-pointer mr-3 ${
                      linkCopied ? 'h-5 w-5' : 'h-5 w-5'
                    }`}
                  />
                }
              />
            </Tooltip>
            <div className="px-6 py-2 text-sm rounded-md bg-finnieBlue-light-tertiary w-fit">
              {referralCode}
            </div>
            {shouldSeeTheNewTag ? (
              <span className="text-xs text-finnieTeal-100">New</span>
            ) : null}
          </>
        ) : (
          <LoadingSpinner className="ml-20 h-9" />
        )}
      </div>

      <div className="mt-6 grid gap-4 xl:grid-cols-2">
        <div className="p-4 rounded-md bg-finnieBlue-light-tertiary">
          <h3 className="mb-3 text-base font-semibold">
            Register a referral link
          </h3>
          <label className="block mb-2 text-xs uppercase tracking-wide opacity-80">
            Referrer wallet
          </label>
          <input
            className="w-full px-3 py-2 mb-3 text-sm rounded bg-finnieBlue-light text-white outline-none"
            value={referrerInput}
            placeholder="Paste referrer wallet"
            onChange={(event) => setReferrerInput(event.target.value)}
          />
          <label className="block mb-2 text-xs uppercase tracking-wide opacity-80">
            Referral code
          </label>
          <input
            className="w-full px-3 py-2 mb-4 text-sm rounded bg-finnieBlue-light text-white outline-none"
            value={referralCodeInput}
            placeholder="Paste referral code"
            onChange={(event) => setReferralCodeInput(event.target.value)}
          />
          <button
            type="button"
            className="px-4 py-2 text-sm font-semibold rounded bg-finnieTeal-100 text-finnieBlue disabled:opacity-40"
            disabled={registerDisabled || registerReferralMutation.isLoading}
            onClick={registerReferral}
          >
            {registerReferralMutation.isLoading
              ? 'Registering...'
              : 'Register Referral'}
          </button>
        </div>

        <div className="p-4 rounded-md bg-finnieBlue-light-tertiary">
          <h3 className="mb-3 text-base font-semibold">This wallet status</h3>
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <span className="block text-xs opacity-70">Wallet</span>
              <span>{compactAddress(mainAccountPubKey)}</span>
            </div>
            <div>
              <span className="block text-xs opacity-70">Referral</span>
              <span>{referralStatusLabel}</span>
            </div>
            <div>
              <span className="block text-xs opacity-70">Referrer</span>
              <span>{compactAddress(referralStatus?.registration?.referrer)}</span>
            </div>
            <div>
              <span className="block text-xs opacity-70">Activity</span>
              <span>
                {referralStatus?.activity_nonce || 0}/
                {referralStatus?.min_referred_account_nonce ||
                  rewardPoolStatus?.min_referred_account_nonce ||
                  1}
              </span>
            </div>
          </div>
          <p className="mt-3 text-xs opacity-80">
            {referralStatusError
              ? 'Referral status is unavailable from QSDM Core.'
              : referralStatus?.message || 'No referral status message yet.'}
          </p>
          <button
            type="button"
            className="px-4 py-2 mt-4 text-sm font-semibold rounded bg-finnieTeal-100 text-finnieBlue disabled:opacity-40"
            disabled={!canClaimReferral || claimReferralMutation.isLoading}
            onClick={claimReferral}
          >
            {claimReferralMutation.isLoading
              ? 'Releasing...'
              : claimsEnabled
              ? 'Release Referral Reward'
              : 'Payouts Locked'}
          </button>
        </div>
      </div>

      {manualStatus ? (
        <p className="px-4 py-3 mt-4 text-sm rounded-md bg-finnieBlue-light-tertiary">
          {manualStatus}
        </p>
      ) : null}
    </div>
  );
}
