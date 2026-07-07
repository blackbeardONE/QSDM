import { useCallback, useState } from 'react';

export const useClipboard = () => {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState('');

  const copyToClipboard = useCallback(async (text: string) => {
    setCopyError('');
    try {
      if (window.main?.copyTextToClipboard) {
        await window.main.copyTextToClipboard({ text });
      } else if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        throw new Error('Clipboard access is unavailable');
      }

      setCopied(true);
      setTimeout(() => {
        setCopied(false);
      }, 2000);
      return true;
    } catch (error) {
      setCopied(false);
      setCopyError(
        error instanceof Error ? error.message : 'Could not copy to clipboard'
      );
      return false;
    }
  }, []);

  return {
    copied,
    copyError,
    copyToClipboard,
  };
};
