import React, { useEffect } from 'react';
import { Navigate } from 'react-router-dom';

import { LoadingScreen } from 'renderer/components';
import { AppRoute } from 'renderer/types/routes';

import { useAccounts, useUserAppConfig } from './features/settings/hooks';

function AppLoader(): JSX.Element {
  const {
    userConfig: settings,
    isUserConfigLoading: loadingSettings,
    handleSaveUserAppConfig,
  } = useUserAppConfig();
  const { accounts, loadingAccounts } = useAccounts();

  const hasLocalAccount = !!accounts?.length;
  const hasUnlockPin = !!settings?.pin;
  const hasCompletedOnboarding =
    !!settings?.onboardingCompleted || (hasLocalAccount && hasUnlockPin);

  useEffect(() => {
    if (
      !loadingSettings &&
      !loadingAccounts &&
      hasCompletedOnboarding &&
      !settings?.onboardingCompleted
    ) {
      handleSaveUserAppConfig({ settings: { onboardingCompleted: true } });
    }
  }, [
    handleSaveUserAppConfig,
    hasCompletedOnboarding,
    loadingAccounts,
    loadingSettings,
    settings?.onboardingCompleted,
  ]);

  const routeToNavigate = (() => {
    if (hasCompletedOnboarding) return AppRoute.AppInit;
    if (hasLocalAccount && !hasUnlockPin) return AppRoute.OnboardingCreatePin;
    return AppRoute.OnboardingInitialScreen;
  })();

  if (loadingSettings || loadingAccounts) {
    return <LoadingScreen />;
  }

  return <Navigate to={routeToNavigate} />;
}

export default AppLoader;
