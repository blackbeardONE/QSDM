import {
  CheckSuccessLine,
  Icon,
  WarningTalkLine,
} from 'vendor/qsdm-styleguide';
import React from 'react';
import { useNavigate } from 'react-router-dom';

import { Button } from 'renderer/components/ui/Button';
import { AppRoute } from 'renderer/types/routes';

export function PhraseSaveSuccess() {
  const navigate = useNavigate();

  const handleContinue = () => {
    navigate(AppRoute.OnboardingGetFreeTokens);
  };

  return (
    <div className="text-center flex flex-col gap-[4vh]">
      <h1 className="flex flex-col items-center justify-center gap-4 text-2xl md2h:text-3xl 2xl:text-3xl font-semibold max-w-[284px] xl:max-w-none mx-auto">
        <div className="text-finnieEmerald-light">
          <Icon source={CheckSuccessLine} className="w-12 h-12 m-3" />
        </div>
        You successfully saved your legacy Hive profile phrase
      </h1>
      <div className="mb-12 mx-auto font-light">
        <p className="mb-6 font-normal">Never share this phrase.</p>
        If you ever need this legacy Hive profile on another device, use your{' '}
        <br /> profile phrase to restore it. You shouldn’t enter your
        <br /> phrase for any other reason.
      </div>
      <div className="flex items-center gap-4 text-[#FFA54B] text-[11px] text-left font-light mx-auto w-fit">
        <Icon source={WarningTalkLine} className="w-6 h-6" />
        CELL wallet recovery uses your QSDM keystore JSON and passphrase.
        <br /> No one from QSDM Hive will ever ask you for either secret.
      </div>

      <Button
        onClick={handleContinue}
        label="Next"
        className="font-semibold bg-finnieGray-light text-finnieBlue-light w-[220px] h-[38px] mx-auto"
      />
    </div>
  );
}
