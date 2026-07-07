import { Button } from 'vendor/qsdm-styleguide';
import { ChevronRightIcon } from '@radix-ui/react-icons';
import React from 'react';
import { useNavigate } from 'react-router-dom';

import QsdmHiveLogo from 'assets/svgs/qsdm-hive-logo.svg';
import { useBrandLogo } from 'renderer/features/common/hooks/useBrandLogo';
import { AppRoute } from 'renderer/types/routes';

import { onboardingSteps } from '../OnboardingLayout/OnboardingLayout';

function InitialScreen() {
  const navigate = useNavigate();

  const startOnboarding = () => navigate(AppRoute.OnboardingCreatePin);

  const BrandLogo = useBrandLogo(QsdmHiveLogo);

  return (
    <div className="qsdm-cell-screen w-full h-full">
      <div className="relative z-10 w-full h-full m-auto flex flex-col items-center justify-center gap-[4vh] md2h:gap-[6vh]">
        <div className="qsdm-cell-card grid h-20 w-20 place-items-center rounded-xl">
          <BrandLogo className="w-14 h-14 text-white" />
        </div>
        <div className="text-xs font-bold uppercase text-[#f7bf42]">
          QSDM Hive / CELL Network
        </div>
        <div className="w-fit text-center font-semibold text-2xl">
          <span className="text-white">Get started in just </span>
          <span className="text-[#7ce8ef]">three simple steps.</span>
        </div>

        <div className="flex gap-10 my-10">
          {onboardingSteps.map(({ label, Icon }) => (
            <StepBox key={label} label={label} Icon={Icon} />
          ))}
        </div>
        <Button
          onClick={startOnboarding}
          label="Start now!"
          labelClassesOverrides="font-semibold"
          buttonClassesOverrides="w-[182px] flex items-center justify-between"
          iconRight={<ChevronRightIcon className="text-finnieBlue w-4 h-4" />}
        />
      </div>
    </div>
  );
}

export default InitialScreen;

interface StepBoxProps {
  label: string;
  Icon: React.FunctionComponent<React.SVGAttributes<SVGElement>>;
}

function StepBox({ label, Icon }: StepBoxProps) {
  return (
    <div className="qsdm-cell-card flex flex-col justify-evenly text-center w-[192px] h-[172px] px-5 rounded-lg">
      <Icon className="w-10 h-10 mx-auto text-[#7ce8ef]" />
      <span className="text-white">{label}</span>
    </div>
  );
}
