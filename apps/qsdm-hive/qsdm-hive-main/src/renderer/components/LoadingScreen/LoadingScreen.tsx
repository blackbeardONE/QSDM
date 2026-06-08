import { Icon } from 'vendor/qsdm-styleguide';
import { faWifi3 } from '@fortawesome/free-solid-svg-icons';
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome';
import React from 'react';
import { twMerge } from 'tailwind-merge';

import WelcomeLinesDiagonal from 'assets/svgs/welcome-lines-diagonal.svg';
import WelcomeWheelBackground from 'assets/svgs/welcome-wheel-background.svg';
import { useBrandLogo } from 'renderer/features/common/hooks/useBrandLogo';
import { useInternetConnectionStatus } from 'renderer/features/settings/hooks/useInternetConnectionStatus';
import { useTheme } from 'renderer/theme/ThemeContext';

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

  const { theme } = useTheme();
  const isVip = theme === 'vip';

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
      <div
        className={twMerge(
          'w-[226px] h-2 rounded-full z-10',
          isVip ? 'bg-[#FFC78F]/40' : 'bg-[#5ED9D1]/40'
        )}
      >
        <div
          className={twMerge(
            'w-full h-full rounded-full progress-bar',
            isVip ? 'bg-[#FFC78F]/90' : 'bg-[#5ED9D1]/90'
          )}
        />
      </div>
    );
  };

  const BrandLogo = useBrandLogo();

  return (
    <div
      className={twMerge(
        'relative flex flex-col items-center justify-center h-full gap-5 overflow-hidden overflow-y-auto text-white',
        isVip
          ? 'bg-[url(assets/svgs/vip-pattern.svg)] bg-top before:absolute before:inset-0 before:bg-gradient-to-b before:from-[#383838]/70 before:via-black before:to-black before:pointer-events-none before:z-0'
          : 'bg-main-gradient'
      )}
    >
      <WelcomeWheelBackground className="absolute top-0 -left-[40%] h-[40%] scale-110 text-finnieTeal-100 z-10" />

      <Icon source={BrandLogo} className="h-[156px] w-[156px] relative z-10" />
      <h1 className="text-[40px] leading-[48px] text-center font-semibold relative z-10">
        Welcome to QSDM Hive.
      </h1>
      <h2 className="text-lg text-center font-semibold relative z-10">
        Decentralized node operations for everyday operators
      </h2>
      <p className="justify-center max-w-xl text-center relative z-10">
        <span className="mr-1 text-finnieTeal">
          <span className="font-semibold">{randomFact.bold}</span>
          {randomFact.normal}
        </span>
      </p>

      <WelcomeLinesDiagonal className="absolute bottom-0 -right-[22.5%] h-full z-10" />

      {getContent()}
    </div>
  );
}
