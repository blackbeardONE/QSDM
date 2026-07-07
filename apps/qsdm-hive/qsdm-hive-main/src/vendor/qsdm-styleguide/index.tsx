import React from 'react';
import { twMerge } from 'tailwind-merge';

type IconProps = React.SVGProps<SVGSVGElement>;

type IconSource =
  | React.ComponentType<IconProps>
  | string
  | undefined
  | null;

type IconWrapperProps = {
  source?: IconSource;
  size?: number;
  color?: string;
  className?: string;
} & React.HTMLAttributes<HTMLElement>;

export enum ButtonSize {
  SM = 'sm',
  MD = 'md',
  LG = 'lg',
  Small = 'sm',
  Medium = 'md',
  Large = 'lg',
}

export enum ButtonVariant {
  Primary = 'primary',
  Secondary = 'secondary',
  SecondaryDark = 'secondary-dark',
  Danger = 'danger',
  Outline = 'outline',
  Ghost = 'ghost',
}

type ButtonProps = React.ButtonHTMLAttributes<HTMLButtonElement> & {
  label?: React.ReactNode;
  loading?: boolean;
  size?: ButtonSize | string;
  variant?: ButtonVariant | string;
  iconLeft?: React.ReactNode;
  iconRight?: React.ReactNode;
  buttonClassesOverrides?: string;
  labelClassesOverrides?: string;
};

export function Button({
  label,
  children,
  loading,
  iconLeft,
  iconRight,
  buttonClassesOverrides,
  labelClassesOverrides,
  className,
  disabled,
  type = 'button',
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      className={twMerge(
        'inline-flex items-center justify-center gap-2 rounded-md px-4 py-2 text-sm font-semibold transition',
        'bg-finnieEmerald-light text-purple-3 hover:brightness-105',
        'disabled:cursor-not-allowed disabled:opacity-50',
        buttonClassesOverrides,
        className
      )}
      {...props}
    >
      {loading ? (
        'Loading...'
      ) : (
        <>
          {iconLeft}
          {label ? <span className={labelClassesOverrides}>{label}</span> : children}
          {iconRight}
        </>
      )}
    </button>
  );
}

export function Icon({
  source: Source,
  size = 20,
  color = 'currentColor',
  className,
  ...props
}: IconWrapperProps) {
  const style = { width: size, height: size, color };

  if (!Source) {
    return <span className={className} style={style} {...props} />;
  }

  if (typeof Source === 'string') {
    return (
      <img
        src={Source}
        alt=""
        className={className}
        style={{ width: size, height: size }}
      />
    );
  }

  return (
    <Source
      className={className}
      style={style}
      {...(props as unknown as IconProps)}
    />
  );
}

function makeLineIcon(paths: React.ReactNode) {
  return function QsdmLineIcon(props: IconProps) {
    return (
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        {...props}
      >
        {paths}
      </svg>
    );
  };
}

function makeFillIcon(paths: React.ReactNode) {
  return function QsdmFillIcon(props: IconProps) {
    return (
      <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" {...props}>
        {paths}
      </svg>
    );
  };
}

export const AddLine = makeLineIcon(
  <>
    <path d="M12 5v14" />
    <path d="M5 12h14" />
  </>
);

export const BrowseInternetLine = makeLineIcon(
  <>
    <circle cx="12" cy="12" r="9" />
    <path d="M3 12h18" />
    <path d="M12 3c2.5 2.4 3.7 5.4 3.7 9s-1.2 6.6-3.7 9" />
    <path d="M12 3c-2.5 2.4-3.7 5.4-3.7 9s1.2 6.6 3.7 9" />
  </>
);

export const CheckSuccessLine = makeLineIcon(
  <>
    <circle cx="12" cy="12" r="9" />
    <path d="m7.5 12 3 3 6-6" />
  </>
);

export const CheckSuccessFill = makeFillIcon(
  <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm-1.2 13.7-4-4 1.4-1.4 2.6 2.6 5-5 1.4 1.4-6.4 6.4Z" />
);

export const ChevronArrowLine = makeLineIcon(<path d="m15 18-6-6 6-6" />);

export const CloseLine = makeLineIcon(
  <>
    <path d="M18 6 6 18" />
    <path d="m6 6 12 12" />
  </>
);

export const CloseFill = makeFillIcon(
  <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm3.8 12.4-1.4 1.4-2.4-2.4-2.4 2.4-1.4-1.4 2.4-2.4-2.4-2.4 1.4-1.4 2.4 2.4 2.4-2.4 1.4 1.4-2.4 2.4 2.4 2.4Z" />
);

export const CopyLine = makeLineIcon(
  <>
    <rect x="8" y="8" width="11" height="11" rx="2" />
    <path d="M5 15H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v1" />
  </>
);

export const CurrencyMoneyLine = makeLineIcon(
  <>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 7v10" />
    <path d="M9 9.5c0-1.2 1.2-2 3-2s3 .8 3 2-1 1.8-3 2.5-3 1.3-3 2.5 1.2 2 3 2 3-.8 3-2" />
  </>
);

export const CurrencyMoneyFill = makeFillIcon(
  <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm1 16h-2v-1.1c-1.7-.2-3-1.1-3-2.8h2c0 .7.7 1.1 2 1.1s2-.4 2-1.1-.7-.9-2.3-1.4C9.8 12.1 8 11.3 8 9.3c0-1.6 1.2-2.7 3-3V5h2v1.2c1.8.3 3 1.4 3 3.1h-2c0-.8-.7-1.3-2-1.3s-2 .5-2 1.2.6.9 2.3 1.4c2 .6 3.7 1.4 3.7 3.4 0 1.7-1.2 2.8-3 3V18Z" />
);

export const DeleteTrashXlLine = makeLineIcon(
  <>
    <path d="M3 6h18" />
    <path d="M8 6V4h8v2" />
    <path d="m19 6-1 14H6L5 6" />
    <path d="M10 11v5" />
    <path d="M14 11v5" />
  </>
);

export const FlagReportLine = makeLineIcon(
  <>
    <path d="M5 21V4" />
    <path d="M5 4h10l-1 4 1 4H5" />
  </>
);

export const HideEyeLine = makeLineIcon(
  <>
    <path d="M3 3l18 18" />
    <path d="M10.6 10.6a2 2 0 0 0 2.8 2.8" />
    <path d="M9.9 5.1A9.9 9.9 0 0 1 12 5c5 0 8.5 5 9.5 7a15.2 15.2 0 0 1-2.1 3" />
    <path d="M6.3 6.8A15.4 15.4 0 0 0 2.5 12c1 2 4.5 7 9.5 7 1.3 0 2.5-.3 3.5-.8" />
  </>
);

export const InformationCircleLine = makeLineIcon(
  <>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 11v5" />
    <path d="M12 7h.01" />
  </>
);

export const KeyUnlockLine = makeLineIcon(
  <>
    <circle cx="7.5" cy="14.5" r="3.5" />
    <path d="M10 12 21 1" />
    <path d="m16 6 2 2" />
    <path d="m13.5 8.5 2 2" />
  </>
);

export const LockLine = makeLineIcon(
  <>
    <rect x="5" y="10" width="14" height="10" rx="2" />
    <path d="M8 10V7a4 4 0 0 1 8 0v3" />
  </>
);

export const PauseFill = makeFillIcon(
  <>
    <rect x="7" y="5" width="4" height="14" rx="1" />
    <rect x="13" y="5" width="4" height="14" rx="1" />
  </>
);

export const PlayFill = makeFillIcon(<path d="M8 5v14l11-7-11-7Z" />);

export const SeedSecretPhraseXlLine = makeLineIcon(
  <>
    <path d="M12 3v18" />
    <path d="M5 8c4 0 7 2 7 7" />
    <path d="M19 8c-4 0-7 2-7 7" />
  </>
);

export const SettingsLine = makeLineIcon(
  <>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1A1.7 1.7 0 0 0 15 19.4a1.7 1.7 0 0 0-1 .2V20a2 2 0 1 1-4 0v-.4a1.7 1.7 0 0 0-1-.2 1.7 1.7 0 0 0-1.9.3l-.1.1A2 2 0 1 1 4.2 17l.1-.1A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-.2-1H4a2 2 0 1 1 0-4h.4c.1-.3.1-.7.2-1a1.7 1.7 0 0 0-.3-1.9l-.1-.1A2 2 0 1 1 7 4.2l.1.1A1.7 1.7 0 0 0 9 4.6c.3-.1.7-.2 1-.2V4a2 2 0 1 1 4 0v.4c.3 0 .7.1 1 .2a1.7 1.7 0 0 0 1.9-.3l.1-.1A2 2 0 1 1 19.8 7l-.1.1A1.7 1.7 0 0 0 19.4 9c.1.3.2.7.2 1h.4a2 2 0 1 1 0 4h-.4c0 .3-.1.7-.2 1Z" />
  </>
);

export const ShareArrowLine = makeLineIcon(
  <>
    <path d="M4 12v7a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-7" />
    <path d="M12 16V4" />
    <path d="m7 9 5-5 5 5" />
  </>
);

export const TooltipChatQuestionLeftLine = makeLineIcon(
  <>
    <path d="M4 5a3 3 0 0 1 3-3h10a3 3 0 0 1 3 3v8a3 3 0 0 1-3 3h-5l-5 4v-4a3 3 0 0 1-3-3V5Z" />
    <path d="M12 12h.01" />
    <path d="M10.8 8.7a1.6 1.6 0 1 1 2.4 1.4c-.7.4-1.2.8-1.2 1.6" />
  </>
);

export const TooltipChatQuestionRightLine = TooltipChatQuestionLeftLine;
export const TooltipChatQuestionLeftFill = TooltipChatQuestionLeftLine;

export const UploadLine = makeLineIcon(
  <>
    <path d="M12 16V4" />
    <path d="m7 9 5-5 5 5" />
    <path d="M5 20h14" />
  </>
);

export const ViewShowLine = makeLineIcon(
  <>
    <path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z" />
    <circle cx="12" cy="12" r="3" />
  </>
);

export const WarningCircleLine = makeLineIcon(
  <>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 7v6" />
    <path d="M12 17h.01" />
  </>
);

export const WarningTriangleLine = makeLineIcon(
  <>
    <path d="M10.3 4.2 2.7 18a2 2 0 0 0 1.7 3h15.2a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0Z" />
    <path d="M12 9v4" />
    <path d="M12 17h.01" />
  </>
);

export const WarningTriangleFill = makeFillIcon(
  <path d="M10.3 4.2 2.7 18a2 2 0 0 0 1.7 3h15.2a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0ZM11 9h2v5h-2V9Zm0 7h2v2h-2v-2Z" />
);

export const ProgressLine = makeLineIcon(
  <>
    <path d="M12 3a9 9 0 1 1-8.5 6" />
    <path d="M3 3v6h6" />
  </>
);

export const TipGiveLine = CurrencyMoneyLine;
export const ClickXlLine = makeLineIcon(
  <>
    <path d="M9 3v10l3-2 2 5 3-1-2-5h4L9 3Z" />
    <path d="M4 4 2 2" />
    <path d="M4 12H1" />
    <path d="M12 4V1" />
  </>
);
export const WarningTalkLine = WarningCircleLine;
export const FavoriteStarLine = makeLineIcon(
  <path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2L12 17.3l-5.6 2.9 1.1-6.2L3 9.6l6.2-.9L12 3Z" />
);
export const FavoriteStarFill = makeFillIcon(
  <path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2L12 17.3l-5.6 2.9 1.1-6.2L3 9.6l6.2-.9L12 3Z" />
);
export const ExtentionPuzzleLine = makeLineIcon(
  <path d="M8 3h5v4h3a3 3 0 1 1 0 6h-3v8H8v-4H5a3 3 0 1 1 0-6h3V3Z" />
);
export const ExtentionPuzzleFill = ExtentionPuzzleLine;
export const LockFill = makeFillIcon(
  <path d="M8 10V7a4 4 0 0 1 8 0v3h1a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2h1Zm2 0h4V7a2 2 0 1 0-4 0v3Z" />
);
export const NotificationOnLine = makeLineIcon(
  <>
    <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9" />
    <path d="M10 21h4" />
  </>
);
export const NotificationOffFill = makeFillIcon(
  <path d="M3.3 2 22 20.7 20.7 22l-4-4H3c0-2 3-2 3-9 0-1 .2-2 .6-2.9L2 3.3 3.3 2ZM18 9c0 1.8.2 3.1.6 4.1L8.2 2.7A6 6 0 0 1 18 9Z" />
);
export const SettingsFill = SettingsLine;
export const WalletFill = CurrencyMoneyFill;
export const GroupPeopleLine = makeLineIcon(
  <>
    <circle cx="8" cy="8" r="3" />
    <circle cx="17" cy="9" r="2.5" />
    <path d="M2 20a6 6 0 0 1 12 0" />
    <path d="M14 20a5 5 0 0 1 8 0" />
  </>
);
export const RewardsEarnLine = FavoriteStarLine;
export const WebCursorXlLine = ClickXlLine;
export const RemoveLine = makeLineIcon(<path d="M5 12h14" />);
