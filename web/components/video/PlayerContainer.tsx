import { FC, useCallback, useEffect, useRef, useState } from 'react';
import classNames from 'classnames';
import { useAtomValue } from 'jotai';

import { clientConfigStateAtom } from '../stores/ClientConfigStore';
import { AutoplaySetting } from '../../utils/autoplay';
import { Localization } from '../../types/localization';
import { WHEPError } from '../../services/whep-client';
import { ConnectWHEPRequest, WHEPSessionHandle } from '../../services/whep-client';
import { Translation } from '../ui/Translation/Translation';
import { CustomPlayerLoader } from '../../services/custom-player-service';
import { CustomPlayerHost } from './custom/CustomPlayerHost';
import { OwncastPlayer } from './OwncastPlayer/OwncastPlayer';
import { WebRTCPlayer } from './webrtc/WebRTCPlayer';
import styles from './PlayerContainer.module.scss';

// Remember a failed WebRTC attempt for this tab so a re-render or a
// navigator re-mount cannot force the viewer through another dead session.
export const WEBRTC_FALLBACK_SESSION_KEY = 'owncast_webrtc_fallback';

type Protocol = 'hls' | 'webrtc';

export type PlayerContainerProps = {
  // HLS playlist URL; the unchanged default playback path.
  source: string;
  online: boolean;
  initiallyMuted?: boolean;
  autoplay?: AutoplaySetting;
  title: string;
  className?: string;
  // Test/Storybook overrides. Production never sets these.
  forceProtocol?: Protocol;
  webRTCSessionFactory?: (request: ConnectWHEPRequest) => Promise<WHEPSessionHandle>;
  customPlayerLoader?: CustomPlayerLoader;
  onProtocolChange?: (protocol: Protocol) => void;
};

function readSessionFallback(): boolean {
  try {
    return sessionStorage.getItem(WEBRTC_FALLBACK_SESSION_KEY) === 'true';
  } catch {
    return false;
  }
}

function writeSessionFallback(value: boolean): void {
  try {
    if (value) sessionStorage.setItem(WEBRTC_FALLBACK_SESSION_KEY, 'true');
    else sessionStorage.removeItem(WEBRTC_FALLBACK_SESSION_KEY);
  } catch {
    // Storage can be unavailable (private mode); fallback stays in memory.
  }
}

// PlayerContainer chooses between low-latency WebRTC playback and the existing
// HLS player. The HLS path is byte-for-byte the same component tree as before
// the WebRTC work, and is the fallback whenever WebRTC is unavailable,
// over capacity, denied, times out or fails ICE.
export const PlayerContainer: FC<PlayerContainerProps> = ({
  source,
  online,
  initiallyMuted = false,
  autoplay = AutoplaySetting.Off,
  title,
  className,
  forceProtocol,
  webRTCSessionFactory,
  customPlayerLoader,
  onProtocolChange,
}) => {
  const clientConfig = useAtomValue(clientConfigStateAtom);
  const webrtcConfig = clientConfig.webrtc;
  const [sessionFallback, setSessionFallback] = useState<boolean>(() => readSessionFallback());
  const [fallbackReason, setFallbackReason] = useState<WHEPError | null>(null);
  const [noticeDismissed, setNoticeDismissed] = useState(false);
  const [customPlayerFailed, setCustomPlayerFailed] = useState(false);
  const previousOnlineRef = useRef(online);

  // A new stream start clears a previous WebRTC failure: the server or the
  // network conditions may have changed since.
  useEffect(() => {
    if (!previousOnlineRef.current && online) {
      writeSessionFallback(false);
      setSessionFallback(false);
      setFallbackReason(null);
      setNoticeDismissed(false);
      setCustomPlayerFailed(false);
    }
    previousOnlineRef.current = online;
  }, [online]);

  const webRTCSupported = typeof window !== 'undefined' && typeof RTCPeerConnection !== 'undefined';
  const webRTCConfigured = Boolean(webrtcConfig?.enabled && webrtcConfig?.whepUrl);

  // A valid custom player module replaces the whole player subtree (spec 7.3).
  // If it fails to load/mount, fall back to the standard WebRTC/HLS selection.
  const customPlayerUrl = clientConfig.player?.custom ? clientConfig.player?.url : undefined;
  const useCustomPlayer = Boolean(
    customPlayerUrl && forceProtocol === undefined && !customPlayerFailed,
  );

  let protocol: Protocol = 'hls';
  if (forceProtocol === 'webrtc') {
    protocol = 'webrtc';
  } else if (forceProtocol !== 'hls' && webRTCConfigured && webRTCSupported && !sessionFallback) {
    protocol = 'webrtc';
  }

  const handleWebRTCFallback = useCallback(
    (error: WHEPError) => {
      writeSessionFallback(true);
      setSessionFallback(true);
      setFallbackReason(error);
      setNoticeDismissed(false);
      onProtocolChange?.('hls');
    },
    [onProtocolChange],
  );

  useEffect(() => {
    onProtocolChange?.(protocol);
    // Only report actual protocol changes, not every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [protocol]);

  const fallbackNotice =
    protocol === 'hls' && fallbackReason && !noticeDismissed ? (
      <div className={styles.notice} role="status">
        <Translation
          translationKey={Localization.Frontend.Player.fallbackToHlsNotice}
          defaultText="Low-latency playback is unavailable right now. Switched to the standard stream."
        />
        <button
          type="button"
          className={styles.noticeDismiss}
          aria-label="Dismiss"
          onClick={() => setNoticeDismissed(true)}
        >
          &times;
        </button>
      </div>
    ) : null;

  if (useCustomPlayer && customPlayerUrl) {
    return (
      <div className={classNames(styles.container, className)} data-protocol="custom">
        <CustomPlayerHost
          playerUrl={customPlayerUrl}
          hlsUrl={source}
          whepUrl={webRTCConfigured ? webrtcConfig?.whepUrl : undefined}
          variants={webrtcConfig?.variants}
          online={online}
          autoplay={autoplay}
          initiallyMuted={initiallyMuted}
          title={title}
          loader={customPlayerLoader}
          onFallback={() => setCustomPlayerFailed(true)}
        />
      </div>
    );
  }

  if (protocol === 'webrtc') {
    return (
      <div className={classNames(styles.container, className)} data-protocol="webrtc">
        <WebRTCPlayer
          whepUrl={webrtcConfig?.whepUrl || ''}
          variants={webrtcConfig?.variants}
          iceServers={webrtcConfig?.iceServers}
          online={online}
          autoplay={autoplay}
          initiallyMuted={initiallyMuted}
          title={title}
          sessionFactory={webRTCSessionFactory}
          onFallback={handleWebRTCFallback}
        />
      </div>
    );
  }

  return (
    <div className={classNames(styles.container, className)} data-protocol="hls">
      <OwncastPlayer
        source={source}
        online={online}
        initiallyMuted={initiallyMuted}
        autoplay={autoplay}
        title={title}
      />
      {fallbackNotice}
    </div>
  );
};
