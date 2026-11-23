import { FC, useCallback, useEffect, useRef, useState } from 'react';
import classNames from 'classnames';
import { useAtomValue } from 'jotai';

import { clientConfigStateAtom } from '../stores/ClientConfigStore';
import { AutoplaySetting } from '../../utils/autoplay';
import { WHEPError } from '../../services/whep-client';
import { ConnectWHEPRequest, WHEPSessionHandle } from '../../services/whep-client';
import { CustomPlayerLoader } from '../../services/custom-player-service';
import { CustomPlayerHost } from './custom/CustomPlayerHost';
import { OwncastPlayer } from './OwncastPlayer/OwncastPlayer';
import { WebRTCPlayer } from './webrtc/WebRTCPlayer';
import { TransportPreference } from './PlayerControls/types';
import {
  readTransportPreference,
  resolveTransport,
  writeTransportPreference,
} from './PlayerControls/preferences';
import styles from './PlayerContainer.module.scss';

// Remember a failed WebRTC attempt for this tab so a re-render or a
// navigator re-mount cannot force the viewer through another dead session.
export const WEBRTC_FALLBACK_SESSION_KEY = 'owncast_webrtc_fallback';

type Protocol = 'hls' | 'webrtc';

export type PlayerContainerProps = {
  // HLS playlist URL; the unchanged default playback path.
  source: string;
  online: boolean;
  // Identifies the current stream. The server changes it on every new publish, so it is what
  // tells one stream apart from the next when the page never sees an offline gap.
  streamIdentity?: string;
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

// The stored fallback remembers which stream failed with it. A different stream means a new
// publisher, a new encoder and possibly a working WebRTC path, so the old failure says nothing
// about it. Without this a page that never observed the offline gap kept the previous stream's
// HLS fallback for the next one.
function readSessionFallback(streamIdentity: string): boolean {
  try {
    const raw = sessionStorage.getItem(WEBRTC_FALLBACK_SESSION_KEY);
    if (!raw) return false;
    const stored = JSON.parse(raw) as { identity?: string; value?: boolean };
    return stored.value === true && stored.identity === streamIdentity;
  } catch {
    return false;
  }
}

function writeSessionFallback(value: boolean, streamIdentity: string): void {
  try {
    if (value) {
      sessionStorage.setItem(
        WEBRTC_FALLBACK_SESSION_KEY,
        JSON.stringify({ value: true, identity: streamIdentity }),
      );
    } else {
      sessionStorage.removeItem(WEBRTC_FALLBACK_SESSION_KEY);
    }
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
  streamIdentity = '',
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
  const [transportPreference, setTransportPreference] = useState<TransportPreference>(() =>
    readTransportPreference(),
  );
  const [sessionFallback, setSessionFallback] = useState<boolean>(() =>
    readSessionFallback(streamIdentity),
  );
  const [fallbackReason, setFallbackReason] = useState<WHEPError | null>(null);
  const [customPlayerFailed, setCustomPlayerFailed] = useState(false);
  const previousOnlineRef = useRef(online);

  // A new stream start clears a previous WebRTC failure: the server or the
  // network conditions may have changed since.
  useEffect(() => {
    if (!previousOnlineRef.current && online) {
      writeSessionFallback(false, streamIdentity);
      setSessionFallback(false);
      setFallbackReason(null);
      setCustomPlayerFailed(false);
    }
    previousOnlineRef.current = online;
  }, [online]);

  // A new stream clears the previous one's fallback even when the page never observed the
  // offline gap: a fast switch can go from online to online, and the old failure must not
  // stop the new stream from trying WebRTC.
  const previousIdentityRef = useRef(streamIdentity);
  useEffect(() => {
    if (previousIdentityRef.current === streamIdentity) return;
    previousIdentityRef.current = streamIdentity;
    writeSessionFallback(false, streamIdentity);
    setSessionFallback(false);
    setFallbackReason(null);
  }, [streamIdentity]);

  const webRTCSupported = typeof window !== 'undefined' && typeof RTCPeerConnection !== 'undefined';
  const webRTCConfigured = Boolean(webrtcConfig?.enabled && webrtcConfig?.whepUrl);

  // A valid custom player module replaces the whole player subtree (spec 7.3).
  // If it fails to load/mount, fall back to the standard WebRTC/HLS selection.
  const customPlayerUrl = clientConfig.player?.custom ? clientConfig.player?.url : undefined;
  const useCustomPlayer = Boolean(
    customPlayerUrl && forceProtocol === undefined && !customPlayerFailed,
  );

  const webRTCAvailable = Boolean(webRTCConfigured && webRTCSupported);
  let protocol: Protocol = 'hls';
  if (forceProtocol === 'webrtc') {
    protocol = 'webrtc';
  } else if (forceProtocol !== 'hls') {
    protocol = resolveTransport(transportPreference, {
      webrtcAvailable: webRTCAvailable,
      sessionFallback,
    });
  }

  // Persisted transport choice (Auto/WebRTC/HLS) from the overlay's transport
  // menu. Selecting WebRTC/Auto clears a previous tab-scoped fallback so the
  // viewer can retry the low-latency path deliberately.
  const handleTransportChange = useCallback((next: TransportPreference) => {
    setTransportPreference(next);
    writeTransportPreference(next);
    if (next !== 'hls') {
      writeSessionFallback(false, streamIdentity);
      setSessionFallback(false);
      setFallbackReason(null);
    }
  }, []);

  const handleWebRTCFallback = useCallback(
    (error: WHEPError) => {
      writeSessionFallback(true, streamIdentity);
      setSessionFallback(true);
      setFallbackReason(error);
      onProtocolChange?.('hls');
    },
    // The identity belongs in the dependencies: without it the callback keeps the identity of
    // the stream that mounted it, and a failure after a switch stores the previous stream's
    // fallback, which then applies to the wrong stream.
    [onProtocolChange, streamIdentity],
  );

  useEffect(() => {
    onProtocolChange?.(protocol);
    // Only report actual protocol changes, not every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [protocol]);

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

  // The player root must be the element Content's Row renders as its direct
  // child (className topSectionElement, id="player") so custom themes that
  // target "#player" and ".ant-row:has(> #player)" apply to both transports.
  if (protocol === 'webrtc') {
    return (
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
        transport={transportPreference}
        webrtcAvailable={webRTCAvailable}
        onTransportChange={handleTransportChange}
        className={className}
      />
    );
  }

  return (
    <OwncastPlayer
      source={source}
      online={online}
      initiallyMuted={initiallyMuted}
      autoplay={autoplay}
      title={title}
      transport={transportPreference}
      webrtcAvailable={webRTCAvailable}
      onTransportChange={handleTransportChange}
      transportError={protocol === 'hls' ? fallbackReason?.message : undefined}
      className={className}
    />
  );
};
