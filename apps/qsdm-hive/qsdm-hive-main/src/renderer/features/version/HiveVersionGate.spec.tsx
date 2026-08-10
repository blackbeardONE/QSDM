import { act, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { useQuery } from 'react-query';

import { checkAppUpdate } from 'renderer/services';

import { HiveVersionGate } from './HiveVersionGate';

jest.mock('react-query', () => ({
  useQuery: jest.fn(),
}));

jest.mock('renderer/components', () => ({
  LoadingScreen: () => <div>Loading Hive</div>,
}));

jest.mock('renderer/services', () => ({
  QueryKeys: { HiveVersionPolicy: 'HiveVersionPolicy' },
  checkAppUpdate: jest.fn(),
  getHiveVersionPolicy: jest.fn(),
  openBrowserWindow: jest.fn(),
  quitApp: jest.fn(),
}));

const oldVersionPolicy = {
  compatible: false,
  updateRequired: true,
  currentVersion: '1.4.11',
  requiredVersion: '1.4.12',
  downloadUrl: 'https://qsdm.tech/downloads/qsdm-hive-1.4.12.exe',
  reason: 'version-mismatch',
};

describe('HiveVersionGate updater integration', () => {
  let updateDownloaded: () => void;
  const removeUpdateAvailable = jest.fn();
  const removeUpdateDownloaded = jest.fn();

  beforeEach(() => {
    updateDownloaded = () => undefined;
    removeUpdateAvailable.mockClear();
    removeUpdateDownloaded.mockClear();
    (checkAppUpdate as jest.Mock).mockReset();
    (checkAppUpdate as jest.Mock).mockResolvedValue({
      isUpdateAvailable: true,
      updateInfo: { version: '1.4.12' },
    });
    Object.defineProperty(window, 'main', {
      configurable: true,
      value: {
        onAppUpdate: jest.fn(() => removeUpdateAvailable),
        onAppDownloaded: jest.fn((callback: () => void) => {
          updateDownloaded = callback;
          return removeUpdateDownloaded;
        }),
      },
    });
  });

  it('starts an automatic update whenever the approved release is newer', async () => {
    (useQuery as jest.Mock).mockReturnValue({
      data: oldVersionPolicy,
      isLoading: false,
      isFetching: false,
      refetch: jest.fn(),
    });

    render(
      <HiveVersionGate>
        <div>Protected Hive</div>
      </HiveVersionGate>
    );

    await waitFor(() => expect(checkAppUpdate).toHaveBeenCalledTimes(1));
    expect(
      await screen.findByText(/Downloading and verifying QSDM Hive 1.4.12/)
    ).toBeInTheDocument();
    expect(screen.queryByText('Protected Hive')).not.toBeInTheDocument();
  });

  it('shows the verified ready state even while the dashboard is blocked', async () => {
    (useQuery as jest.Mock).mockReturnValue({
      data: oldVersionPolicy,
      isLoading: false,
      isFetching: false,
      refetch: jest.fn(),
    });

    render(
      <HiveVersionGate>
        <div>Protected Hive</div>
      </HiveVersionGate>
    );
    await waitFor(() => expect(checkAppUpdate).toHaveBeenCalledTimes(1));

    act(() => updateDownloaded());
    expect(
      screen.getByText(/The update is verified and ready/)
    ).toBeInTheDocument();
  });

  it('offers the installer instead of claiming a download that never started', async () => {
    (checkAppUpdate as jest.Mock).mockResolvedValue({
      isUpdateAvailable: false,
      updateInfo: { version: '1.4.11' },
    });
    (useQuery as jest.Mock).mockReturnValue({
      data: oldVersionPolicy,
      isLoading: false,
      isFetching: false,
      refetch: jest.fn(),
    });

    render(
      <HiveVersionGate>
        <div>Protected Hive</div>
      </HiveVersionGate>
    );

    expect(
      await screen.findByText(/automatic updater did not offer/i)
    ).toBeInTheDocument();
    expect(screen.getByText('Download Installer')).toBeInTheDocument();
    expect(screen.queryByText('Protected Hive')).not.toBeInTheDocument();
  });

  it('keeps update listeners isolated when the gate unmounts', () => {
    (useQuery as jest.Mock).mockReturnValue({
      data: { ...oldVersionPolicy, compatible: true, reason: 'current' },
      isLoading: false,
      isFetching: false,
      refetch: jest.fn(),
    });

    const view = render(
      <HiveVersionGate>
        <div>Protected Hive</div>
      </HiveVersionGate>
    );
    expect(screen.getByText('Protected Hive')).toBeInTheDocument();

    view.unmount();
    expect(removeUpdateAvailable).toHaveBeenCalledTimes(1);
    expect(removeUpdateDownloaded).toHaveBeenCalledTimes(1);
  });
});
