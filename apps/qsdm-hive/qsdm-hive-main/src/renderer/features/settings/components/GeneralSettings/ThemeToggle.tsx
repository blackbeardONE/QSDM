import React from 'react';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { useTheme } from 'renderer/theme/ThemeContext';

import { SettingSwitch } from '../MainSettings/SettingSwitch';

export function ThemeToggle() {
  const { theme, setTheme } = useTheme();

  return (
    <div className="flex flex-col gap-5">
      <SettingSwitch
        id="theme-toggle"
        isLoading={false}
        isChecked={theme === 'vip'}
        onSwitch={() => setTheme(theme === 'vip' ? 'qsdm' : 'vip')}
        labels={[NATIVE_TOKEN_SYMBOL, 'VIP']}
      />
    </div>
  );
}
