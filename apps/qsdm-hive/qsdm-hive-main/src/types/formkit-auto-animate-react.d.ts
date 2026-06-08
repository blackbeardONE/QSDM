declare module '@formkit/auto-animate/react' {
  import { RefObject } from 'react';

  export function useAutoAnimate<T extends Element = HTMLDivElement>(
    options?: unknown
  ): RefObject<T>;
}
