import { FC, useCallback, useEffect, useRef, useState } from 'react';
import classNames from 'classnames';
import { useHotkeys } from 'react-hotkeys-hook';
import { useTranslation } from 'next-export-i18n';
import PlayCircleFilled from '@ant-design/icons/PlayCircleFilled';
import PauseCircleFilled from '@ant-design/icons/PauseCircleFilled';
import SoundFilled from '@ant-design/icons/SoundFilled';
import MutedFilled from '@ant-design/icons/MutedFilled';
import FullscreenOutlined from '@ant-design/icons/FullscreenOutlined';

import { autoplayModeForSetting, shouldShowBigUnmuteOverlay } from '../../../utils/autoplay';
import { getLocalStorage, setLocalStorage } from '../../../utils/localStorage';
import { Localization } from '../../../types/localization';
import { Translation } from '../../ui/Translation/Translation';
import { VideoPoster } from '../VideoPoster/VideoPoster';
import { WHEPIceServer } from '../../../interfaces/whep.model';
import { WebRTCVariantInfo } from '../../../interfaces/client-config.model';
import { WHEPError } from '../../../services/whep-client';
import { ConnectWHEPRequest, WHEPSessionHandle, connectWHEP } from '../../../services/whep-client';
import { startWebRTCStatsBeacon, WebRTCStatsBeacon } from './webrtc-stats';
import styles from './WebRTCPlayer.module.scss';

const PLAYER_VOLUME = 'owncast_volume';
const AUTOPLAY_FALLBACK_VOLUME = 0.7;

export type WebRTCPlayerProps = {
  whepUrl: string;
  variants?: WebRTCVariantInfo[];
  online: boolean;
  autoplay?: string;
  initiallyMuted?: boolean;
  title: string;
  className?: string;
  iceServers?: WHEPIceServer[];
  onFallback?: (error: WHEPError) => void;
  onReady?: (session: WHEPSessionHandle) => void;
  // Injectable for tests and Storybook; defaults to the real WHEP client.
  sessionFactory?: (request: ConnectWHEPRequest) => Promise<WHEPSessionHandle>;
  statsEndpoint?: string;
  statsIntervalMs?: number;
  enableStats?: boolean;
};

type Phase = 'idle' | 'connecting' | 'ready' | 'error';

// Localized text for attributes that cannot render a Translation component
// (aria-labels). Falls back to English when the catalog has no entry yet.
function useLabel(key: string, fallback: string): string {
  const { t } = useTranslation();
  const translated = t(key);
  return translated === key ? fallback : translated;
}

export const WebRTCPlayer: FC<WebRTCPlayerProps> = ({
  whepUrl,
  variants = [],
  online,
  autoplay = 'off',
  initiallyMuted = false,
  title,
  className,
  iceServers,
  onFallback,
  onReady,
  sessionFactory,
  statsEndpoint,
  statsIntervalMs,
  enableStats = true,
}) => {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const sessionRef = useRef<WHEPSessionHandle | null>(null);
  const statsRef = useRef<WebRTCStatsBeacon | null>(null);
  const mutedByAutoplayRef = useRef(false);
  const startedMutedByViewerRef = useRef(false);
  const onFallbackRef = useRef(onFallback);
  const onReadyRef = useRef(onReady);

  const [phase, setPhase] = useState<Phase>('idle');
  const [errorMessage, setErrorMessage] = useState<string>('');
  const [isPlaying, setIsPlaying] = useState(false);
  const [isMuted, setIsMuted] = useState(false);
  const [bigUnmuteVisible, setBigUnmuteVisible] = useState(false);
  const [selectedVariant, setSelectedVariant] = useState<number | null>(null);
  const [retryToken, setRetryToken] = useState(0);

  const playLabel = useLabel(Localization.Frontend.Player.play, 'Play');
  const pauseLabel = useLabel(Localization.Frontend.Player.pause, 'Pause');
  const muteLabel = useLabel(Localization.Frontend.unmute, 'Unmute');
  const muteToggleLabel = useLabel(Localization.Frontend.Player.mute, 'Mute');
  const muteButtonLabel = isMuted ? muteLabel : muteToggleLabel;
  const fullscreenLabel = useLabel(Localization.Frontend.Player.fullscreen, 'Fullscreen');
  const qualityLabel = useLabel(Localization.Frontend.Player.quality, 'Quality');
  const unmuteLabel = useLabel(Localization.Frontend.unmute, 'Unmute');
  const retryLabel = useLabel(Localization.Frontend.Player.retry, 'Retry');

  useEffect(() => {
    onFallbackRef.current = onFallback;
    onReadyRef.current = onReady;
  }, [onFallback, onReady]);

  // Persist volume the same way the video.js player does so the viewer's
  // preference survives a protocol switch.
  useEffect(() => {
    const savedVolume = getLocalStorage(PLAYER_VOLUME);
    startedMutedByViewerRef.current = savedVolume !== null && Number(savedVolume) === 0;
  }, []);

  const updateBigUnmute = useCallback(() => {
    const video = videoRef.current;
    if (!video) {
      setBigUnmuteVisible(false);
      return;
    }
    setBigUnmuteVisible(
      shouldShowBigUnmuteOverlay({
        mutedByAutoplay: mutedByAutoplayRef.current,
        paused: video.paused,
        muted: video.muted,
        volume: video.volume,
      }),
    );
  }, []);

  const handleVolumeChange = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    setIsMuted(video.muted || video.volume === 0);
    setLocalStorage(PLAYER_VOLUME, video.muted ? 0 : video.volume);
    if (!video.muted && video.volume > 0) {
      mutedByAutoplayRef.current = false;
    }
    updateBigUnmute();
  }, [updateBigUnmute]);

  const attemptAutoplay = useCallback(async () => {
    const video = videoRef.current;
    if (!video) return;

    const savedVolume = getLocalStorage(PLAYER_VOLUME);
    const saved = savedVolume !== null ? Number(savedVolume) : NaN;
    video.volume = Number.isFinite(saved) && saved > 0 ? saved : 1;
    video.muted = initiallyMuted || startedMutedByViewerRef.current;

    const mode = autoplayModeForSetting(autoplay, {
      prefersReducedMotion:
        typeof window.matchMedia === 'function'
          ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
          : false,
      saveData:
        (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData ===
        true,
      startsInaudible: video.muted || video.volume === 0,
    });

    if (mode === false) {
      return;
    }

    if (mode === 'play') {
      // Never start silently: if playback would begin inaudible there is no
      // meaningful "sound only" autoplay attempt.
      if (video.muted || video.volume === 0) return;
      try {
        await video.play();
      } catch {
        // Browser blocked audible autoplay; stay paused.
      }
      return;
    }

    // 'any': try with sound first, then fall back to muted playback. Only the
    // muted fallback counts as "the player muted itself" for the overlay.
    try {
      await video.play();
    } catch {
      if (video.muted) {
        // Already requested to start muted; this is not the fallback path.
        try {
          await video.play();
        } catch {
          // Autoplay blocked entirely.
        }
        return;
      }
      video.muted = true;
      try {
        await video.play();
        mutedByAutoplayRef.current = !startedMutedByViewerRef.current;
      } catch {
        // Autoplay blocked entirely.
      }
    }
    updateBigUnmute();
  }, [autoplay, initiallyMuted, updateBigUnmute]);

  // Attach/detach the peer connection session. Runs on URL/variant/online
  // changes and tears the previous session down before reconnecting.
  useEffect(() => {
    const video = videoRef.current;
    const sessionFactoryImpl = sessionFactory || connectWHEP;

    if (!online || !whepUrl) {
      sessionRef.current = null;
      if (video) video.srcObject = null;
      setPhase('idle');
      setIsPlaying(false);
      return () => {};
    }

    const controller = new AbortController();
    let cancelled = false;
    let localSession: WHEPSessionHandle | null = null;

    const handlePlaying = () => {
      setIsPlaying(true);
      setPhase('ready');
      updateBigUnmute();
    };
    const handlePause = () => {
      setIsPlaying(false);
      updateBigUnmute();
    };

    setPhase('connecting');
    setErrorMessage('');
    setIsPlaying(false);

    sessionFactoryImpl({
      whepUrl,
      variantIndex: selectedVariant,
      iceServers,
      signal: controller.signal,
    })
      .then(async session => {
        if (cancelled) {
          await session.deleteSession();
          session.close();
          return;
        }
        localSession = session;
        sessionRef.current = session;

        if (video) {
          video.srcObject = session.stream;
          video.addEventListener('playing', handlePlaying);
          video.addEventListener('pause', handlePause);
          video.addEventListener('volumechange', handleVolumeChange);
        }

        setPhase('ready');
        onReadyRef.current?.(session);

        if (enableStats) {
          statsRef.current = startWebRTCStatsBeacon({
            pc: session.pc,
            sessionId: session.sessionUrl,
            intervalMs: statsIntervalMs,
            endpoint: statsEndpoint,
          });
        }

        try {
          await attemptAutoplay();
        } catch {
          // Autoplay failure is not a playback failure; the viewer can press play.
        }
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        const whepError =
          error instanceof WHEPError
            ? error
            : new WHEPError('signaling', (error as Error)?.message || 'WebRTC playback failed');
        setPhase('error');
        setErrorMessage(whepError.message);
        setIsPlaying(false);
        // Expected playback failures are not errors worth a console.error:
        // the container falls back to HLS on them.
        if (onFallbackRef.current) {
          onFallbackRef.current(whepError);
        } else {
          console.warn('WebRTC playback failed:', whepError.code, whepError.message);
        }
      });

    return () => {
      cancelled = true;
      controller.abort();
      statsRef.current?.stop();
      statsRef.current = null;
      if (video) {
        video.removeEventListener('playing', handlePlaying);
        video.removeEventListener('pause', handlePause);
        video.removeEventListener('volumechange', handleVolumeChange);
        video.srcObject = null;
      }
      if (localSession) {
        void localSession.deleteSession();
        localSession.close();
      }
      sessionRef.current = null;
    };
    // The effect intentionally re-runs only for connection-relevant inputs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [whepUrl, online, selectedVariant, retryToken, sessionFactory]);

  const togglePlayback = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) {
      video.play().catch(() => {});
    } else {
      video.pause();
    }
  }, []);

  const toggleMute = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (video.muted || video.volume === 0) {
      mutedByAutoplayRef.current = false;
      video.muted = false;
      if (video.volume === 0) {
        const saved = Number(getLocalStorage(PLAYER_VOLUME));
        video.volume = saved > 0 ? saved : AUTOPLAY_FALLBACK_VOLUME;
      }
    } else {
      video.muted = true;
    }
    handleVolumeChange();
  }, [handleVolumeChange]);

  const toggleFullScreen = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {});
    } else {
      video.requestFullscreen?.().catch(() => {});
    }
  }, []);

  useHotkeys('space', e => {
    e.preventDefault();
    togglePlayback();
  });
  useHotkeys('f', toggleFullScreen, { enableOnContentEditable: false });
  useHotkeys('m', toggleMute, { enableOnContentEditable: false });

  const handleBigUnmute = () => {
    const video = videoRef.current;
    if (!video) return;
    mutedByAutoplayRef.current = false;
    video.muted = false;
    if (video.volume === 0) video.volume = AUTOPLAY_FALLBACK_VOLUME;
    handleVolumeChange();
  };

  const showPoster = !isPlaying;

  return (
    <div className={classNames(styles.container, className)} id="webrtc-player">
      <div className={styles.player}>
        <video
          ref={videoRef}
          className={styles.video}
          aria-label={title}
          playsInline
          controls={false}
        />
        {showPoster && (
          <div className={styles.poster}>
            <VideoPoster online={online} initialSrc="/thumbnail.jpg" src="/thumbnail.jpg" />
          </div>
        )}

        {phase === 'connecting' && (
          <div className={styles.statusOverlay} role="status">
            <Translation
              translationKey={Localization.Frontend.Player.webrtcConnecting}
              defaultText="Connecting to the low-latency stream..."
            />
          </div>
        )}

        {phase === 'error' && (
          <div className={styles.statusOverlay} role="alert">
            <p>{errorMessage}</p>
            <button
              type="button"
              className={styles.retryButton}
              onClick={() => setRetryToken(token => token + 1)}
            >
              {retryLabel}
            </button>
          </div>
        )}

        {phase === 'ready' && !isPlaying && (
          <button
            type="button"
            className={styles.bigPlayButton}
            aria-label={playLabel}
            onClick={togglePlayback}
          >
            <PlayCircleFilled />
          </button>
        )}

        {bigUnmuteVisible && (
          <button
            type="button"
            className={styles.bigUnmuteButton}
            aria-label={unmuteLabel}
            onClick={handleBigUnmute}
          >
            <MutedFilled />
          </button>
        )}

        <div className={styles.controls}>
          <button
            type="button"
            className={styles.controlButton}
            aria-label={isPlaying ? pauseLabel : playLabel}
            onClick={togglePlayback}
          >
            {isPlaying ? <PauseCircleFilled /> : <PlayCircleFilled />}
          </button>
          <button
            type="button"
            className={styles.controlButton}
            aria-label={muteButtonLabel}
            onClick={toggleMute}
          >
            {isMuted ? <MutedFilled /> : <SoundFilled />}
          </button>
          {variants.length > 1 && (
            <label className={styles.variantSelector}>
              <span className={styles.visuallyHidden}>{qualityLabel}</span>
              <select
                aria-label={qualityLabel}
                value={selectedVariant === null ? '' : String(selectedVariant)}
                onChange={event => {
                  const { value } = event.target;
                  setSelectedVariant(value === '' ? null : Number(value));
                }}
              >
                <option value="">
                  <Translation
                    translationKey={Localization.Frontend.Player.qualityAuto}
                    defaultText="Auto"
                  />
                </option>
                {variants.map(variant => (
                  <option key={variant.index} value={String(variant.index)}>
                    {variant.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          <button
            type="button"
            className={styles.controlButton}
            aria-label={fullscreenLabel}
            onClick={toggleFullScreen}
          >
            <FullscreenOutlined />
          </button>
        </div>
      </div>
    </div>
  );
};
