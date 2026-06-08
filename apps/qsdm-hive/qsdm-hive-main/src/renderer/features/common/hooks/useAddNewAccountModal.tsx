import { show } from '@ebay/nice-modal-react';

import { AddNewAccount } from '../modals';

export type AddNewAccountModalOptions = {
  pickCreateByDefault?: boolean;
  hideQsdmSignerImport?: boolean;
};

export const useAddNewAccountModal = (
  optionsOrPickCreateByDefault?: boolean | AddNewAccountModalOptions
) => {
  const options =
    typeof optionsOrPickCreateByDefault === 'boolean'
      ? { pickCreateByDefault: optionsOrPickCreateByDefault }
      : optionsOrPickCreateByDefault ?? {};

  const showModal = (): Promise<string> => {
    return show(AddNewAccount, options);
  };

  return {
    showModal,
  };
};
