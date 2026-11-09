import { FC, useEffect, useRef } from 'react';
import classNames from 'classnames';
// Host-provided transport library for custom player modules. video.js ships
// VHS, which is how HLS plays in Chromium; the module cannot import it itself
// because it is served outside the app bundle.
import videojs from 'video.js';

import { AutoplaySetting } from '../../../utils/autoplay';
import { WebRTCVariantInfo } from '../../../interfaces/client-config.model';
import { OwncastPlayerInstance } from '../../../interfaces/custom-player.model';
import {
  CustomPlayerLoader,
  defaultCustomPlayerLoader,
  loadCustomPlayerModule,
  buildCustomPlayerContext,
  reportCustomPlayerError,
  reportCustomPlayerPlaybackEvent,
} from '../../../services/custom-player-service';
import styles from './CustomPlayerHost.module.scss';

export type CustomPlayerHostProps = {
  // Served module URL; defaults to /custom-player/player.js when omitted.
  playerUrl?: string;
  hlsUrl: string;
  whepUrl?: string;
  variants?: WebRTCVariantInfo[];
  online: boolean;
  autoplay?: AutoplaySetting;
  initiallyMuted?: boolean;
  title?: string;
  className?: string;
  // Called when the module cannot be loaded or mounted; the container then
  // falls back to the default player.
  onFallback: (error: Error) => void;
  // Test/Storybook injection. Production never sets these.
  loader?: CustomPlayerLoader;
  errorReporter?: (message: string) => void;
};

// Hosts the operator-configured custom player module (master spec 7.3).
// Everything else on the page is untouched: this only replaces the player
// subtree. On any load/mount failure the module is reported to the admin
// Appearance page and the caller falls back to the standard player.
export const CustomPlayerHost: FC<CustomPlayerHostProps> = ({
  playerUrl,
  hlsUrl,
  whepUrl,
  variants,
  online,
  autoplay = AutoplaySetting.Off,
  initiallyMuted = false,
  title,
  className,
  onFallback,
  loader,
  errorReporter,
}) => {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const onFallbackRef = useRef(onFallback);
  const loaderRef = useRef<CustomPlayerLoader>();
  const errorReporterRef = useRef<((message: string) => void) | undefined>(errorReporter);

  useEffect(() => {
    onFallbackRef.current = onFallback;
  }, [onFallback]);
  useEffect(() => {
    loaderRef.current = loader;
  }, [loader]);
  useEffect(() => {
    errorReporterRef.current = errorReporter;
  }, [errorReporter]);

  useEffect(() => {
    const element = containerRef.current;
    if (!element || !playerUrl || !online) {
      return undefined;
    }

    let cancelled = false;
    let instance: OwncastPlayerInstance | null = null;

    const fail = (message: string) => {
      if (cancelled) return;
      const text = message || 'The custom player module failed to load.';
      // Expected failure: warn, report, and let the container fall back.
      console.warn('Custom player module failed:', text);
      if (errorReporterRef.current) {
        errorReporterRef.current(text);
      } else {
        void reportCustomPlayerError(text);
      }
      onFallbackRef.current(new Error(text));
    };

    (async () => {
      try {
        const mount = await loadCustomPlayerModule(
          playerUrl,
          loaderRef.current || defaultCustomPlayerLoader,
        );
        if (cancelled) return;

        const context = buildCustomPlayerContext({
          hlsUrl,
          whepUrl,
          variants,
          isOnline: online,
          autoplay,
          initiallyMuted,
          libs: { videojs },
          reportPlayback: event => {
            void reportCustomPlayerPlaybackEvent(event);
          },
        });

        const mounted = mount(element, context);
        if (!mounted || typeof mounted.destroy !== 'function') {
          throw new Error('The custom player mount() must return an object with destroy().');
        }
        instance = mounted;
      } catch (error) {
        fail((error as Error)?.message || 'The custom player module failed to mount.');
      }
    })();

    return () => {
      cancelled = true;
      if (instance) {
        try {
          instance.destroy();
        } catch (error) {
          console.warn('Custom player destroy() threw during cleanup:', error);
        }
        instance = null;
      }
      element.replaceChildren();
    };
    // Mount once per online session: the context is passed at mount time per
    // the contract. Callback props are kept in refs above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [playerUrl, online]);

  return (
    <div
      ref={containerRef}
      className={classNames(styles.host, className)}
      data-custom-player="true"
      aria-label={title}
    />
  );
};

export default CustomPlayerHost;
