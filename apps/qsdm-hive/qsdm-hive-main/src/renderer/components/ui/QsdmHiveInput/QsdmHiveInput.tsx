import React, { ChangeEventHandler } from 'react';
import { twMerge } from 'tailwind-merge';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';

type PropsType = {
  disabled?: boolean;
  onInputChange: ChangeEventHandler<HTMLInputElement>;
  value?: number;
  className?: string;
  symbol?: string;
  initialValue?: number;
};

export function QsdmHiveInput({
  disabled,
  onInputChange,
  value,
  className,
  symbol = NATIVE_TOKEN_SYMBOL,
  initialValue,
}: PropsType) {
  const handleInputChange: ChangeEventHandler<HTMLInputElement> = (e) => {
    onInputChange(e);
  };

  const wrapperDivClasses = twMerge(
    'flex w-[246px] items-center justify-center px-4 py-2 space-x-1 text-white duration-500 rounded-md bg-finnieBlue-light-tertiary transition-width mx-auto',
    className
  );

  return (
    <div className={wrapperDivClasses}>
      <div className="relative flex w-fit">
        <input
          type="number"
          inputMode="numeric"
          value={value ?? initialValue}
          disabled={disabled}
          onChange={handleInputChange}
          placeholder="00"
          className="w-full h-10 text-3xl text-right text-white bg-transparent outline-none max-w-[350px] pr-4"
        />
      </div>
      <span className="relative text-xl right-0">{symbol}</span>
    </div>
  );
}
