import React from 'react';
import { twMerge } from 'tailwind-merge';

export enum LoadingSpinnerSize {
  Small = 'Small',
  Medium = 'Medium',
  Large = 'Large',
  XLarge = 'XLarge',
}

type PropsType = {
  size?: LoadingSpinnerSize;
  className?: string;
};

const getSizeClasses = (size: LoadingSpinnerSize) => {
  const sizes = {
    [LoadingSpinnerSize.Small]: 'w-4 h-4',
    [LoadingSpinnerSize.Medium]: 'w-6 h-6',
    [LoadingSpinnerSize.Large]: 'w-8 h-8',
    [LoadingSpinnerSize.XLarge]: 'w-20 h-20',
  }[size];
  return sizes;
};

export function LoadingSpinner({
  size = LoadingSpinnerSize.Medium,
  className,
}: PropsType) {
  const sizeClasses = getSizeClasses(size);

  const classes = twMerge(
    'block shrink-0 animate-spin rounded-full border-2 border-solid border-current border-t-transparent',
    sizeClasses,
    className
  );

  return (
    <div role="status" className="inline-flex items-center justify-center">
      <span className={classes} aria-hidden="true" />
      <span className="sr-only">Loading...</span>
    </div>
  );
}
