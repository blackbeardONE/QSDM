import { Icon } from 'vendor/qsdm-styleguide';
import { faWifi3 } from '@fortawesome/free-solid-svg-icons';
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome';
import React from 'react';

import { useBrandLogo } from 'renderer/features/common/hooks/useBrandLogo';
import { useInternetConnectionStatus } from 'renderer/features/settings/hooks/useInternetConnectionStatus';

type PropsType = {
  initError?: string;
};

const facts = [
  {
    bold: 'QSDM Hive keeps your node close at hand.',
    normal:
      ' Manage tasks, rewards, wallets, and node health from one desktop app.',
  },
  {
    bold: 'QSDM Hive is built for everyday operators.',
    normal:
      ' Run useful compute, monitor status, and keep your node online with less friction.',
  },
  {
    bold: 'Your computer can support decentralized workloads.',
    normal:
      ' QSDM Hive helps turn idle resources into active network participation.',
  },
  {
    bold: 'QSDM Hive keeps security local.',
    normal: ' Your PIN unlocks access on this device without adding needless steps.',
  },
  {
    bold: 'Task extensions can connect your node to external services.',
    normal: ' Review every extension before enabling it.',
  },
  {
    bold: 'QSDM Hive is designed for long-running nodes.',
    normal: ' Keep the app online to support active tasks and background work.',
  },
  {
    bold: 'A healthy node starts with a stable connection.',
    normal: ' Check your internet status if startup takes longer than expected.',
  },
  {
    bold: 'QSDM Hive gives you one place to operate.',
    normal: ' Track rewards, inspect task status, and manage account access.',
  },
];

const randomFact = facts[Math.floor(Math.random() * facts.length)];

export function LoadingScreen({ initError }: PropsType): JSX.Element {
  const isOnline = useInternetConnectionStatus();

  const getContent = () => {
    if (initError) {
      return (
        <p className="text-lg text-center text-finnieRed">
          <span>
            Something went wrong.
            <br /> Please restart QSDM Hive
          </span>
        </p>
      );
    }

    if (!isOnline) {
      return (
        <p className="text-lg text-center text-finnieRed">
          <FontAwesomeIcon icon={faWifi3} className="pr-2" />
          <span>
            No internet connection.
            <br /> Please check your connection or restart QSDM Hive
          </span>
        </p>
      );
    }

    return (
      <div className="qsdm-loading-progress">
        <div className="qsdm-loading-progress-fill progress-bar" />
      </div>
    );
  };

  const BrandLogo = useBrandLogo();

  return (
    <div className="qsdm-loading-screen">
      <div className="qsdm-loading-grid" />
      <div className="qsdm-loading-glow qsdm-loading-glow-one" />
      <div className="qsdm-loading-glow qsdm-loading-glow-two" />

      <main className="qsdm-loading-shell">
        <div className="qsdm-loading-mark">
          <span className="qsdm-loading-ring qsdm-loading-ring-one" />
          <span className="qsdm-loading-ring qsdm-loading-ring-two" />
          <span className="qsdm-loading-node qsdm-loading-node-one" />
          <span className="qsdm-loading-node qsdm-loading-node-two" />
          <span className="qsdm-loading-node qsdm-loading-node-three" />
          <Icon source={BrandLogo} className="qsdm-loading-logo" />
        </div>
        <p className="qsdm-loading-eyebrow">QSDM Hive / CELL Network</p>
        <h1 className="qsdm-loading-title">
          Preparing your CELL workspace.
        </h1>
        <h2 className="qsdm-loading-subtitle">
          Wallet, tasks, and node health are coming online.
        </h2>
        <p className="qsdm-loading-fact">
          <span>{randomFact.bold}</span>
          {randomFact.normal}
        </p>

        <div className="qsdm-loading-status">{getContent()}</div>
      </main>
    </div>
  );
}
