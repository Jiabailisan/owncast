/* eslint-disable react/no-danger */
import { FC, useEffect } from 'react';

export const PushNotificationServiceWorker: FC = () => {
  useEffect(() => {
    if (!('serviceWorker' in navigator)) {
      return undefined;
    }

    // In production, /sw.js imports /serviceWorker.js so a single root-scoped
    // worker handles both precaching and push notifications without competing
    // registrations evicting each other.
    const scriptUrl = process.env.NODE_ENV === 'production' ? '/sw.js' : '/serviceWorker.js';
    const add = () => {
      navigator.serviceWorker.register(scriptUrl).then(
        registration => {
          console.debug('Service Worker registration successful with scope: ', registration.scope);
        },
        err => {
          console.error('Service Worker registration failed: ', err);
        },
      );
    };

    if (document.readyState === 'complete') {
      add();
      return undefined;
    }

    window.addEventListener('load', add);
    return () => {
      window.removeEventListener('load', add);
    };
  }, []);

  return null;
};
