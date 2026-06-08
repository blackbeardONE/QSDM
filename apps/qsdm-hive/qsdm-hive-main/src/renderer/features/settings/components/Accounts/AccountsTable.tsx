import { AddLine, Icon } from 'vendor/qsdm-styleguide';
import React, { useMemo } from 'react';

import { LoadingSpinner } from 'renderer/components/ui';
import { useAddNewAccountModal } from 'renderer/features/common/hooks/useAddNewAccountModal';

import { useAccounts } from '../../hooks';
import { useCellPrice } from '../../hooks/useCellPrice';

import { AccountItem } from './AccountItem';

type PropsType = {
  addButtonLabel?: string;
  hideQsdmSignerImport?: boolean;
};

export function AccountsTable({
  addButtonLabel = 'Add new account',
  hideQsdmSignerImport,
}: PropsType) {
  const { showModal } = useAddNewAccountModal({ hideQsdmSignerImport });
  const { accounts, loadingAccounts } = useAccounts();
  const accountsSorted = useMemo(
    () => [...(accounts ?? [])].sort((a) => (a.isDefault ? -1 : 0)),
    [accounts]
  );

  const { data: cellPrice } = useCellPrice();

  if (loadingAccounts) {
    return <LoadingSpinner className="w-40 h-40 mx-auto mt-40" />;
  }

  return (
    <div className="flex flex-col flex-1 h-full min-h-0">
      <div className="flex-1 overflow-y-auto min-h-0">
        {accountsSorted.map(
          ({
            accountName,
            mainPublicKey,
            stakingPublicKey,
            kplStakingPublicKey,
            isDefault,
          }) => (
            <div key={accountName} className="py-4">
              <AccountItem
                systemKey={mainPublicKey}
                stakingKey={stakingPublicKey}
                kplStakingKey={kplStakingPublicKey}
                accountName={accountName}
                isDefault={isDefault}
                cellPrice={cellPrice ?? 0}
              />
            </div>
          )
        )}
      </div>
      <div className="sticky bottom-0 pt-4 pb-0">
        <button
          onClick={showModal}
          className="flex items-center text-sm text-finnieTeal-100 underline-offset-2"
        >
          <Icon source={AddLine} className="h-[18px] w-[18px] mr-2" />
          {addButtonLabel}
        </button>
      </div>
    </div>
  );
}
