import { useEffect, useState } from 'react';

// Reactive CSS media query, used by the admin live viewer table to switch
// between the table and its mobile card-list fallback.
export function useMediaQuery(query: string, defaultValue = false): boolean {
  const [matches, setMatches] = useState<boolean>(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return defaultValue;
    }
    return window.matchMedia(query).matches;
  });

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return undefined;
    }
    const mediaQueryList = window.matchMedia(query);
    setMatches(mediaQueryList.matches);

    const handleChange = (event: MediaQueryListEvent) => setMatches(event.matches);
    // Safari < 14 only supports the deprecated listener API.
    if (typeof mediaQueryList.addEventListener === 'function') {
      mediaQueryList.addEventListener('change', handleChange);
      return () => mediaQueryList.removeEventListener('change', handleChange);
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (mediaQueryList as any).addListener(handleChange);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return () => (mediaQueryList as any).removeListener(handleChange);
  }, [query]);

  return matches;
}

export const MOBILE_BREAKPOINT_QUERY = '(max-width: 768px)';

export default useMediaQuery;
