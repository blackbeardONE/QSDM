import { HideEyeLine, Icon, ViewShowLine } from 'vendor/qsdm-styleguide';
import { AnimatePresence, motion } from 'framer-motion';
import React, { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from 'react-query';
import { useLocation } from 'react-router-dom';
import { twMerge } from 'tailwind-merge';

import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import {
  useAccount,
  useAccountBalance,
} from 'renderer/features/settings/hooks';
import { useStakingAccountRecoveryFlow } from 'renderer/features/settings/hooks/useStakingAccountRecoveryFlow';
import { AccountType } from 'renderer/features/settings/types';
import { useExportSecretPhrase } from 'renderer/features/shared/hooks/useExportSecretPhrase';
import { useKplTokens } from 'renderer/features/tokens/hooks';
import { getCellFromBaseUnits } from 'utils/currencyConversion';

import { AccountBalancesHeader } from './AccountBalanceHeader';
import { CoinList } from './CoinList';
import { LoadingTokenDataErrorDisplay } from './LoadingTokenDataErrorDisplay';
import { RemoveAccount } from './RemoveAccont';
import { WalletKeyInfo } from './WalletKeyInfo';

type PropsType = {
  accountName: string;
  systemKey: string;
  stakingKey: string;
  kplStakingKey?: string;
  isDefault: boolean;
  cellPrice: number;
};

export function AccountItem({
  accountName,
  systemKey,
  stakingKey,
  kplStakingKey,
  isDefault,
  cellPrice,
}: PropsType) {
  const location = useLocation();
  const isQsdmNative = QSDM_BRIDGE_CONFIG.runtimeMode === 'qsdm-native';
  const shouldExpandWalletOnLoad =
    (!!(location?.state as any)?.walletExpanded || false) && isDefault;

  const [isOpened, setIsOpened] = useState(shouldExpandWalletOnLoad);
  const [selectedKey, setSelectedKey] = useState(systemKey);
  const { deleteAccount, setAccountActive, setAccountActiveLoading } =
    useAccount({ accountName, isDefault });
  const { accountBalance = 0 } = useAccountBalance(systemKey);
  const { accountBalance: stakingAccountBalance = 0 } =
    useAccountBalance(stakingKey);
  const { accountBalance: kplStakingAccountBalance = 0 } =
    useAccountBalance(kplStakingKey);

  useEffect(() => {
    if (isDefault) {
      setIsOpened(true);
    }
  }, [isDefault]);

  const {
    kplTokenItems,
    isLoadingTokensData,
    kpLBalanceError,
    isKpLBalanceFetching,
  } = useKplTokens({
    publicKey: selectedKey,
    enabled: !isQsdmNative,
  });

  const { stakingKeyIsMessedUp } = useStakingAccountRecoveryFlow({
    isEnabled: isDefault && !isQsdmNative,
    shouldNotify: false,
    stakingPublicKey: stakingKey,
    isKPLStakingAccount: false,
  });

  const { stakingKeyIsMessedUp: kplStakingKeyIsMessedUp } =
    useStakingAccountRecoveryFlow({
      isEnabled: isDefault && !isQsdmNative,
      shouldNotify: false,
      stakingPublicKey: kplStakingKey,
      isKPLStakingAccount: true,
    });

  const { showModal: showExportSecretPhraseModal } = useExportSecretPhrase({
    accountName,
    publicKey: selectedKey,
  });

  const handleExportSecretPhrase = async () => {
    showExportSecretPhraseModal();
  };

  const accountBalanceInCELL = getCellFromBaseUnits(accountBalance);
  const stakingAccountBalanceInCELL = getCellFromBaseUnits(stakingAccountBalance);
  const kplStakingAccountBalanceInCELL = getCellFromBaseUnits(
    kplStakingAccountBalance
  );

  const queryClient = useQueryClient();

  const ref = useRef<HTMLDivElement>(null);
  const { data: isBalancesHidden } = useQuery({
    queryKey: ['isBalancesHidden', accountName],
    queryFn: () => window.main.getIsAccountHidden({ accountName }),
  });

  const { mutate: toggleBalancesHidden } = useMutation({
    mutationFn: () =>
      window.main.setIsAccountHidden({
        accountName,
        isHidden: !isBalancesHidden,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ['isBalancesHidden', accountName],
      });
    },
  });

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsOpened(false);
      }
    };

    window.addEventListener('keydown', handleKeyDown);

    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, []);

  if (kpLBalanceError) {
    return (
      <LoadingTokenDataErrorDisplay
        error={`We couldn't fetch your balances for the account ${accountName}`}
      />
    );
  }

  const containerVariants = {
    closed: { height: 136 },
    opened: { height: 'auto' },
  };

  const contentVariants = {
    closed: { opacity: 0 },
    opened: { opacity: 1 },
  };

  const selectedAccountType = {
    [systemKey]: 'SYSTEM',
    [stakingKey]: 'STAKING',
    [kplStakingKey || '']: 'KPL_STAKING',
  }[selectedKey] as AccountType;

  const getDollarBalanceByAccountType = (accountType: AccountType) => {
    const getDollarBalance = (price: number, balance: number) => {
      const rawBalance = price ? balance * price : 0;
      return Number(rawBalance.toFixed(2));
    };

    if (accountType === 'SYSTEM') {
      return getDollarBalance(cellPrice, accountBalanceInCELL);
    }
    if (accountType === 'STAKING') {
      return getDollarBalance(cellPrice, stakingAccountBalanceInCELL);
    }
    if (accountType === 'KPL_STAKING') {
      return getDollarBalance(cellPrice, kplStakingAccountBalanceInCELL);
    }
    return 0;
  };

  return (
    <div className="w-[90%]">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <AccountBalancesHeader
            accountName={accountName}
            isDefault={isDefault}
            setAccountActive={setAccountActive}
            setAccountActiveLoading={setAccountActiveLoading}
          />
          <button
            onClick={() => toggleBalancesHidden()}
            className="text-purple-1 hover:text-purple-2 transition-colors"
          >
            {isBalancesHidden ? (
              <Icon source={ViewShowLine} size={20} />
            ) : (
              <Icon source={HideEyeLine} size={20} />
            )}
          </button>
        </div>
      </div>

      <div className="flex flex-row items-center gap-2">
        <motion.div
          className={twMerge(
            'flex justify-between w-[90%] p-3 mt-3 rounded-xl bg-purple-1 bg-opacity-20 min-h-[136px]',
            isOpened ? 'h-full min-h-[485px]' : 'h-[136px] overflow-hidden',
            (stakingKeyIsMessedUp || kplStakingKeyIsMessedUp) &&
              'bg-finnieRed/[.35]'
          )}
          initial="closed"
          animate={isOpened ? 'opened' : 'closed'}
          variants={containerVariants}
          transition={{ duration: 0.3, ease: 'easeInOut' }}
        >
          <AnimatePresence mode="wait">
            <motion.div
              key={isOpened ? 'opened' : 'closed'}
              className="flex flex-col gap-4"
              variants={contentVariants}
              initial="closed"
              animate="opened"
              exit="closed"
              transition={{ duration: 0.2, delay: 0.1 }}
            >
              <WalletKeyInfo
                key={systemKey}
                label={isQsdmNative ? 'Legacy System Key' : 'System Key'}
                tokenValue={isBalancesHidden ? '***' : accountBalanceInCELL}
                tokenUsdValue={
                  isQsdmNative
                    ? undefined
                    : getDollarBalanceByAccountType('SYSTEM')
                }
                walletAddress={systemKey}
                walletType="SYSTEM"
                onShowSeedPhraseModal={handleExportSecretPhrase}
                isSelected={selectedKey === systemKey && !isLoadingTokensData}
                onClick={() => setSelectedKey(systemKey)}
                compact={!isOpened}
                kplTokenItems={kplTokenItems}
                accountName={accountName}
                isKpLBalanceFetching={isKpLBalanceFetching}
                isBalancesHidden={isBalancesHidden}
                nativeSendDisabled={isQsdmNative}
                hideProfilePhraseAction={isQsdmNative}
              />
              {isOpened && (
                <div className="flex flex-col">
                  <motion.div
                    initial={{ opacity: 0, y: -20 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: -20 }}
                    transition={{ duration: 0.2, delay: 0.2 }}
                  >
                    <WalletKeyInfo
                      key={stakingKey}
                      label={
                        isQsdmNative ? 'Legacy Staking Key' : 'Staking Key'
                      }
                      tokenValue={
                        isBalancesHidden ? '***' : stakingAccountBalanceInCELL
                      }
                      tokenUsdValue={
                        isQsdmNative
                          ? undefined
                          : getDollarBalanceByAccountType('STAKING')
                      }
                      walletAddress={stakingKey}
                      walletType="STAKING"
                      onShowSeedPhraseModal={handleExportSecretPhrase}
                      isSelected={
                        selectedKey === stakingKey && !isLoadingTokensData
                      }
                      onClick={() => setSelectedKey(stakingKey)}
                      stakingKeyIsMessedUp={stakingKeyIsMessedUp}
                      kplTokenItems={kplTokenItems}
                      accountName={accountName}
                      isKpLBalanceFetching={isKpLBalanceFetching}
                      isDefault={isDefault}
                      isBalancesHidden={isBalancesHidden}
                      nativeSendDisabled={isQsdmNative}
                      hideProfilePhraseAction={isQsdmNative}
                    />
                  </motion.div>

                  {kplStakingKey && (
                    <motion.div
                      className="mt-4"
                      initial={{ opacity: 0, y: -20 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, y: -20 }}
                      transition={{ duration: 0.2, delay: 0.2 }}
                    >
                      <WalletKeyInfo
                        key={kplStakingKey}
                        label={
                          isQsdmNative
                            ? 'Legacy KPL Staking Key'
                            : 'KPL Staking Key'
                        }
                        tokenValue={
                          isBalancesHidden
                            ? '***'
                            : kplStakingAccountBalanceInCELL
                        }
                        tokenUsdValue={
                          isQsdmNative
                            ? undefined
                            : getDollarBalanceByAccountType('KPL_STAKING')
                        }
                        walletAddress={kplStakingKey}
                        walletType="KPL_STAKING"
                        onShowSeedPhraseModal={handleExportSecretPhrase}
                        isSelected={
                          selectedKey === kplStakingKey && !isLoadingTokensData
                        }
                        onClick={() => setSelectedKey(kplStakingKey)}
                        stakingKeyIsMessedUp={kplStakingKeyIsMessedUp}
                        kplTokenItems={kplTokenItems}
                        accountName={accountName}
                        isKpLBalanceFetching={isKpLBalanceFetching}
                        isDefault={isDefault}
                        isBalancesHidden={isBalancesHidden}
                        nativeSendDisabled={isQsdmNative}
                        hideProfilePhraseAction={isQsdmNative}
                      />
                    </motion.div>
                  )}
                </div>
              )}
            </motion.div>
          </AnimatePresence>

          <CoinList
            availableTokens={kplTokenItems}
            isLoadingTokensData={isLoadingTokensData || isKpLBalanceFetching}
            isOpened={isOpened}
            setIsOpened={setIsOpened}
            walletType={selectedAccountType}
            isBalancesHidden={isBalancesHidden}
          />
        </motion.div>

        {!isDefault && (
          <RemoveAccount
            accountName={accountName}
            onAccountDelete={deleteAccount}
          />
        )}
      </div>
    </div>
  );
}
