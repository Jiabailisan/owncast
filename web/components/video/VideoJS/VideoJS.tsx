import React, { FC } from 'react';
import videojs from 'video.js';
import type VideoJsPlayer from 'video.js/dist/types/player';
import { useTranslation } from 'next-export-i18n';

import styles from './VideoJS.module.scss';

const SHORTCUT_SUFFIXES: Record<string, string> = {
  Play: ' (Space)',
  Pause: ' (Space)',
  Mute: ' (m)',
  Unmute: ' (m)',
  Fullscreen: ' (f)',
  'Non-Fullscreen': ' (f)',
  'Picture-in-Picture': ' (i)',
  'Exit Picture-in-Picture': ' (i)',
};

export type VideoJSProps = {
  options: any;
  onReady: (player: VideoJsPlayer, vjsInstance: typeof videojs) => void;
};

export const VideoJS: FC<VideoJSProps> = ({ options, onReady }) => {
  const videoRef = React.useRef<HTMLVideoElement | null>(null);
  const playerRef = React.useRef<VideoJsPlayer | null>(null);
  const { t } = useTranslation();

  const addShortcutsToLanguage = (vjs: typeof videojs, langCode: string) => {
    const updates: Record<string, string> = {};
    Object.keys(SHORTCUT_SUFFIXES).forEach(key => {
      const currentLabel = key;
      const suffix = SHORTCUT_SUFFIXES[key];
      updates[key] = t(`${currentLabel}${suffix}`);
    });
    vjs.addLanguage(langCode, updates);
  };

  // video.js is an imperative widget: recreate the player when the media
  // source changes and always dispose it on unmount. Without this, switching
  // between WebRTC and HLS (or reconnecting a player) leaked players and could
  // leave two audio tracks playing.
  const source = options?.sources?.[0]?.src || '';

  React.useEffect(() => {
    const videoElement = videoRef.current;
    if (!videoElement) {
      return undefined;
    }

    addShortcutsToLanguage(videojs, 'en');
    const finalOptions = {
      ...options,
      noUITitleAttributes: true, // Prevents videojs from adding a title attribute to UI elements, thus preventing "double tooltips".
    };
    const player: VideoJsPlayer = (playerRef.current = videojs(videoElement, finalOptions, () => {
      console.debug('player is ready');
      return onReady && onReady(player, videojs);
    }));

    player.autoplay(options.autoplay);
    player.src(options.sources);

    const onXhrHooksReady = () => {
      const cachebusterRequestHook = o => {
        const { uri } = o;
        let updatedURI = uri;
        if (o.uri.match('m3u8')) {
          const u = uri.startsWith('http')
            ? new URL(uri)
            : new URL(uri, window.location.protocol + window.location.host);
          const cachebuster = Math.random().toString(16).slice(2, 8);
          u.searchParams.append('cachebust', cachebuster);
          updatedURI = u.toString();
        }
        return {
          ...o,
          uri: updatedURI,
        };
      };
      (player.tech({ IWillNotUseThisInPlugins: true }) as any)?.vhs?.xhr?.onRequest(
        cachebusterRequestHook,
      );
    };
    player.on('xhr-hooks-ready', onXhrHooksReady);

    return () => {
      try {
        player.off('xhr-hooks-ready', onXhrHooksReady);
      } catch (e) {
        console.warn('Unable to remove video.js listener during cleanup', e);
      }
      if (playerRef.current === player) {
        playerRef.current = null;
      }
      try {
        player.dispose();
      } catch (e) {
        console.warn('Unable to dispose video.js player', e);
      }
    };
    // Recreate only when the media source changes: the player components build
    // a fresh options object on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source]);

  return (
    <div data-vjs-player>
      {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
      <video
        ref={videoRef}
        className={`video-js vjs-big-play-centered vjs-show-big-play-button-on-pause ${styles.player} vjs-owncast`}
      />
    </div>
  );
};
