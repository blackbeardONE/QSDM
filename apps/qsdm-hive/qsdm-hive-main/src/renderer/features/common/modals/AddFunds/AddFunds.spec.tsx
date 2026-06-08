import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';

import { SidebarActions } from 'renderer/features/sidebar/components';
import { render } from 'renderer/tests/utils';

const publicKey = 'myPublicKey';

Object.defineProperty(window, 'main', {
  value: {
    getMainAccountPubKey: jest.fn(() => Promise.resolve(publicKey)),
    getQsdmCellAccount: jest.fn(() =>
      Promise.resolve({
        configured: true,
        reachable: true,
        apiUrl: 'http://127.0.0.1:8080/api/v1',
        dashboardUrl: 'http://127.0.0.1:8081',
        tokenSymbol: 'CELL',
        address: publicKey,
        balance: 0,
        checkedAt: new Date().toISOString(),
      })
    ),
    openBrowserWindow: jest.fn(),
    claimQsdmCellFaucet: jest.fn(() =>
      Promise.resolve({
        address: publicKey,
        status: 'funded',
        amount_granted: 25,
        balance_before: 0,
        balance_after: 25,
        target_balance: 25,
        source: 'local-cell-faucet',
        checked_at: new Date().toISOString(),
      })
    ),
  },
});

const copyToClipboard = jest.fn();
Object.defineProperty(navigator, 'clipboard', {
  value: {
    writeText: copyToClipboard,
  },
});

const renderSidebar = () => {
  render(
    <SidebarActions
      onPrimaryActionClick={() => {
        return '';
      }}
      onSecondaryActionClick={() => {
        return '';
      }}
    />
  );
};

describe('AddFunds', () => {
  it('claims starter CELL from QSDM Core when clicking the claim button', async () => {
    renderSidebar();

    const addFundsButton = screen.getByTestId('sidebar_tip_give_button');
    await userEvent.click(addFundsButton);

    const getMyFreeTokensButton = await screen.findByText(/Claim CELL/i);
    await userEvent.click(getMyFreeTokensButton);

    expect(window.main.claimQsdmCellFaucet).toHaveBeenCalledWith({
      address: publicKey,
    });
  });

  it('copies the public key to the clipboard when clicking on the `copy` button', async () => {
    renderSidebar();

    const addFundsButton = screen.getByTestId('sidebar_tip_give_button');
    await userEvent.click(addFundsButton);

    const copyButton = await screen.findByText(publicKey);
    await userEvent.click(copyButton);

    expect(copyToClipboard).toHaveBeenCalledWith(publicKey);
  });
});
