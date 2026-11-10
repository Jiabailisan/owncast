import { FC, useCallback, useEffect, useRef, useState } from 'react';
import classNames from 'classnames';
import { useHotkeys } from 'react-hotkeys-hook';
import { useTranslation } from 'next-export-i18n';
import MutedFilled from '@ant-design/icons/MutedFilled';

import { autoplayModeForSetting, shouldShowBigUnmuteOverlay } from '../../../utils/autoplay';
import { getLocalStorage, setLocalStorage } from '../../../utils/localStorage';
import { Localization } from '../../../types/localization';
import { Translation } from '../../ui/Translation/Translation';
import { VideoPoster } from '../VideoPoster/VideoPoster';
import { WHEPIceServer } from '../../../interfaces/whep.model';
import { WebRTCVariantInfo } from '../../../interfaces/client-config.model';
import { WHEPError } from '../../../services/whep-client';
import { ConnectWHEPRequest, WHEPSessionHandle, connectWHEP } from '../../../services/whep-client';
import {
  startWebRTCStatsBeacon,
  WebRTCStatsBeacon,
  collectWebRTCStatsPanel,
  collectVideoInboundTotals,
  chooseEncodedWebRTCVariant,
  packetLossPctFor,
  VideoInboundTotals,
  AUTO_QUALITY_SWITCH_LOSS_PCT,
  AUTO_QUALITY_SWITCH_SAMPLES,
  AUTO_QUALITY_SAMPLE_INTERVAL_MS,
  formatWebRTCResolution,
  plausibleFrameRate,
  summarizeSDP,
  webRTCLiveMode,
} from './webrtc-stats';
import { PlayerControls } from '../PlayerControls/PlayerControls';
import {
  TransportPreference,
  StatsSnapshot,
  SparklineState,
  makeEmptyStats,
} from '../PlayerControls/types';
import { formatViewportFrames, formatVolume, pushSparkline } from '../PlayerControls/stats';
import { LATENCY_STORAGE_KEY } from '../PlayerControls/preferences';
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
  // Overlay player controls (ported from the provided binary).
  transport?: TransportPreference;
  webrtcAvailable?: boolean;
  onTransportChange?: (transport: TransportPreference) => void;
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
  variants,
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
  transport = 'auto',
  webrtcAvailable = true,
  onTransportChange = () => {},
}) => {
  const containerRef = useRef<HTMLDivElement | null>(null);
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
  const [volume, setVolume] = useState(1);
  const [bigUnmuteVisible, setBigUnmuteVisible] = useState(false);
  const [selectedVariant, setSelectedVariant] = useState<number | null>(null);
  const [retryToken, setRetryToken] = useState(0);

  const [fullscreen, setFullscreen] = useState(false);
  const [latencyEnabled, setLatencyEnabled] = useState(
    () => getLocalStorage(LATENCY_STORAGE_KEY) === 'true',
  );
  const [statsOpen, setStatsOpen] = useState(false);
  const [stats, setStats] = useState<StatsSnapshot>(() => makeEmptyStats(true));
  const [sparklines, setSparklines] = useState<SparklineState>({ video: [], audio: [] });

  const unmuteLabel = useLabel(Localization.Frontend.unmute, 'Unmute');
  const retryLabel = useLabel(Localization.Frontend.Player.retry, 'Retry');

  // WebRTC exposes the same quality ladder HLS has; "Auto" lets the server pick
  // the default variant, while an explicit row pins a WHEP variant. Selecting a
  // row reconnects the WHEP session with ?variant=<index>.
  const qualityOptions = (variants || []).map(variant => ({
    id: String(variant.index),
    name: variant.name,
  }));
  const selectedQuality = selectedVariant === null ? 'auto' : String(selectedVariant);
  const selectedVariantRef = useRef(selectedVariant);
  useEffect(() => {
    selectedVariantRef.current = selectedVariant;
  }, [selectedVariant]);

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

  useEffect(() => {
    const onFullscreenChange = () => setFullscreen(Boolean(document.fullscreenElement));
    document.addEventListener('fullscreenchange', onFullscreenChange);
    return () => document.removeEventListener('fullscreenchange', onFullscreenChange);
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
    setVolume(video.volume);
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
      if (video.muted || video.volume === 0) return;
      try {
        await video.play();
      } catch {
        // Browser blocked audible autoplay; stay paused.
      }
      return;
    }

    try {
      await video.play();
    } catch {
      if (video.muted) {
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

  // A configuration refresh can remove the selected variant; fall back to Auto
  // instead of connecting to a nonexistent WHEP variant.
  useEffect(() => {
    if (selectedVariant !== null && !(variants || []).some(v => v.index === selectedVariant)) {
      setSelectedVariant(null);
    }
  }, [variants, selectedVariant]);

  // Auto keeps the passthrough variant while it is healthy. If its packet loss
  // stays above the threshold the viewer would otherwise see a frozen picture
  // (commonly the high-bitrate source copy on a lossy link), so reconnect to an
  // encoded variant instead. An explicit user quality choice is never overridden.
  useEffect(() => {
    if (!online || phase !== 'ready' || selectedVariant !== null) {
      return undefined;
    }
    const encodedVariant = chooseEncodedWebRTCVariant(variants);
    if (!encodedVariant) {
      return undefined;
    }

    let cancelled = false;
    let previous: VideoInboundTotals | null = null;
    let consecutiveBadSamples = 0;
    const timer = window.setInterval(async () => {
      const session = sessionRef.current;
      if (!session) return;
      try {
        const totals = await collectVideoInboundTotals(session.pc);
        if (cancelled || selectedVariantRef.current !== null) return;
        if (previous) {
          const lossPct = packetLossPctFor(previous, totals);
          const receivedDelta = totals.packetsReceived - previous.packetsReceived;
          if (lossPct !== null && receivedDelta >= 30) {
            if (lossPct > AUTO_QUALITY_SWITCH_LOSS_PCT) {
              consecutiveBadSamples += 1;
            } else {
              consecutiveBadSamples = 0;
            }
            if (consecutiveBadSamples >= AUTO_QUALITY_SWITCH_SAMPLES) {
              setSelectedVariant(encodedVariant.index);
              return;
            }
          }
        }
        previous = totals;
      } catch {
        // Stats are best-effort; leave the session untouched.
      }
    }, AUTO_QUALITY_SAMPLE_INTERVAL_MS);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [online, phase, selectedVariant, variants]);

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
    setStats(prev => ({ ...prev, collecting: true }));

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
          // The server keys sessions by the bare resource id; the WHEP client
          // exposes the full Location path. Reporting the path made every
          // beacon 404 (and logged a console error), so send only the id.
          const sessionId =
            session.sessionUrl.split('/').filter(Boolean).pop() || session.sessionUrl;
          statsRef.current = startWebRTCStatsBeacon({
            pc: session.pc,
            sessionId,
            intervalMs: statsIntervalMs ?? 2000,
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

  // Minimize latency applies real receiver-side playout limits. jitterBufferTarget
  // (Chrome) and playoutDelayHint (Firefox/older Chrome) both cap the receiver
  // buffer; the catch-up loop nudges playbackRate when the estimated playout
  // latency still exceeds one second.
  useEffect(() => {
    if (phase !== 'ready') {
      return undefined;
    }
    const session = sessionRef.current;
    if (!session) {
      return undefined;
    }
    const receivers =
      typeof session.pc.getReceivers === 'function'
        ? (session.pc.getReceivers() as (RTCRtpReceiver & {
            jitterBufferTarget?: number;
            playoutDelayHint?: number;
          })[])
        : [];
    receivers.forEach(receiver => {
      try {
        if ('jitterBufferTarget' in receiver) {
          receiver.jitterBufferTarget = latencyEnabled ? 200 : 1000;
        }
      } catch {
        // Not supported by this browser.
      }
      try {
        if ('playoutDelayHint' in receiver) {
          receiver.playoutDelayHint = latencyEnabled ? 0.2 : 1;
        }
      } catch {
        // Not supported by this browser.
      }
    });

    let timer: ReturnType<typeof setInterval> | null = null;
    let lateSamples = 0;
    if (latencyEnabled) {
      timer = setInterval(() => {
        const video = videoRef.current;
        const activeSession = sessionRef.current;
        if (!video || !activeSession || video.paused) return;
        void activeSession.pc.getStats().then(report => {
          const liveLatency = collectWebRTCStatsPanel(report).sample.liveLatencySeconds;
          if (liveLatency === null) return;
          if (liveLatency > 1) {
            lateSamples += 1;
            if (lateSamples >= 2 && video.playbackRate === 1) {
              video.playbackRate = 1.1;
            }
          } else if (liveLatency < 0.7) {
            lateSamples = 0;
            if (video.playbackRate > 1) video.playbackRate = 1;
          }
        });
      }, 1000);
    }

    return () => {
      if (timer) clearInterval(timer);
      const video = videoRef.current;
      if (video && video.playbackRate !== 1) video.playbackRate = 1;
    };
  }, [latencyEnabled, phase, selectedVariant]);

  // Sample WebRTC stats at 1 Hz while the Stats for nerds panel is open.
  useEffect(() => {
    if (!statsOpen || phase !== 'ready') {
      return undefined;
    }
    let cancelled = false;
    let previousBytes: ReturnType<typeof collectWebRTCStatsPanel>['bytes'] | null = null;
    const sample = async () => {
      const session = sessionRef.current;
      if (!session) return;
      try {
        const report = await session.pc.getStats();
        if (cancelled) return;
        const { sample: panelSample, bytes } = collectWebRTCStatsPanel(report, previousBytes);
        previousBytes = bytes;
        const video = videoRef.current;
        const volumeInfo = formatVolume(video?.volume ?? 1, Boolean(video?.muted));
        // Current / Optimal Res: WxH@<fps>fps when a plausible frame rate is
        // known (reference formatting); Live Mode shows the selected ICE
        // transport (WebRTC-U for UDP / WebRTC-T for TCP).
        const fps = plausibleFrameRate(panelSample.framesPerSecond);
        const resolution = formatWebRTCResolution(
          panelSample.frameWidth,
          panelSample.frameHeight,
          fps,
        );
        setStats({
          collecting: false,
          isWebRTC: true,
          streamId: 'WHEP / MediaStream',
          viewport: formatViewportFrames(
            typeof window === 'undefined' ? 0 : window.innerWidth,
            typeof window === 'undefined' ? 0 : window.innerHeight,
            panelSample.framesDropped,
            panelSample.framesDecoded,
          ),
          frames: formatViewportFrames(
            typeof window === 'undefined' ? 0 : window.innerWidth,
            typeof window === 'undefined' ? 0 : window.innerHeight,
            panelSample.framesDropped,
            panelSample.framesDecoded,
          ),
          resolution,
          optimalResolution: resolution,
          volume: volumeInfo.volume,
          normalizedVolume: volumeInfo.normalizedVolume,
          codecs:
            (panelSample.videoCodec || 'H.264') +
            ' / ' +
            (panelSample.audioCodec || 'OPUS') +
            ' (WebRTC)',
          sdp: summarizeSDP(session.pc.remoteDescription?.sdp),
          qualityName:
            selectedVariant === null
              ? 'Auto'
              : (variants || []).find(v => v.index === selectedVariant)?.name ||
                String(selectedVariant),
          videoBitrateKbps: panelSample.videoBitrateKbps,
          audioBitrateKbps: panelSample.audioBitrateKbps,
          connectionSpeedKbps: panelSample.connectionSpeedKbps,
          rttMs: panelSample.rttMs,
          jitterMs: panelSample.jitterMs,
          packetsLost: panelSample.packetsLost,
          packetsReceived: panelSample.packetsReceived,
          bufferSeconds: null,
          liveLatencySeconds: panelSample.liveLatencySeconds,
          liveMode: webRTCLiveMode(panelSample.transport),
          date: new Date().toLocaleString(),
        });
        setSparklines(current => ({
          video: pushSparkline(current.video, panelSample.videoBitrateKbps),
          audio: pushSparkline(current.audio, panelSample.audioBitrateKbps),
        }));
      } catch {
        // getStats can fail while the peer connection is closing.
      }
    };
    void sample();
    const timer = setInterval(() => void sample(), 1000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [statsOpen, phase]);

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

  const setPlayerVolume = useCallback(
    (next: number) => {
      const video = videoRef.current;
      if (!video) return;
      video.muted = false;
      video.volume = Math.max(0, Math.min(1, next));
      handleVolumeChange();
    },
    [handleVolumeChange],
  );

  const toggleFullScreen = useCallback(() => {
    const container = containerRef.current;
    const video = videoRef.current;
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {});
      return;
    }
    // iPhone/iPad Safari does not implement the standard Element.requestFullscreen
    // on arbitrary elements; its video element exposes webkitEnterFullscreen.
    const videoElement = video as
      (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null;
    if (videoElement?.webkitEnterFullscreen) {
      videoElement.webkitEnterFullscreen();
      return;
    }
    const target = (container || video) as
      (HTMLElement & { webkitRequestFullscreen?: () => Promise<void> | void }) | null;
    if (!target) return;
    if (target.requestFullscreen) {
      target.requestFullscreen().catch(() => {});
    } else if (target.webkitRequestFullscreen) {
      Promise.resolve(target.webkitRequestFullscreen()).catch(() => {});
    }
  }, []);

  const togglePip = useCallback(() => {
    const video = videoRef.current as
      | (HTMLVideoElement & {
          webkitSupportsPresentationMode?: (mode: string) => boolean;
          webkitSetPresentationMode?: (mode: string) => void;
        })
      | null;
    if (!video) return;
    // Safari uses the WebKit presentation-mode API instead of the standard
    // requestPictureInPicture API.
    if (video.webkitSupportsPresentationMode?.('picture-in-picture')) {
      video.webkitSetPresentationMode?.(
        video.webkitSupportsPresentationMode('picture-in-picture')
          ? 'picture-in-picture'
          : 'inline',
      );
      return;
    }
    if (!document.pictureInPictureEnabled) return;
    if (document.pictureInPictureElement) {
      document.exitPictureInPicture().catch(() => {});
    } else {
      video.requestPictureInPicture?.().catch(() => {});
    }
  }, []);

  const goLive = useCallback(() => {
    // A MediaStream is always delivered live; nothing to seek back.
  }, []);

  const toggleLatency = useCallback(() => {
    setLatencyEnabled(current => {
      const next = !current;
      setLocalStorage(LATENCY_STORAGE_KEY, next);
      return next;
    });
  }, []);

  useHotkeys('space', e => {
    e.preventDefault();
    togglePlayback();
  });
  useHotkeys('f', toggleFullScreen, { enableOnContentEditable: false });
  useHotkeys('m', toggleMute, { enableOnContentEditable: false });
  useHotkeys(
    '0',
    () => {
      const video = videoRef.current;
      if (video) setPlayerVolume(video.volume + 0.1);
    },
    { enableOnContentEditable: false },
  );
  useHotkeys(
    '9',
    () => {
      const video = videoRef.current;
      if (video) setPlayerVolume(video.volume - 0.1);
    },
    { enableOnContentEditable: false },
  );

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
    <div
      ref={containerRef}
      className={classNames(styles.container, className)}
      id="player"
      data-protocol="webrtc"
    >
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

        <PlayerControls
          playing={isPlaying}
          muted={isMuted}
          volume={volume}
          transport={transport}
          activeTransport="webrtc"
          webrtcAvailable={webrtcAvailable}
          qualities={qualityOptions}
          selectedQuality={selectedQuality}
          showQuality={qualityOptions.length > 0}
          latencyEnabled={latencyEnabled}
          latencySupported
          liveEdge
          loading={phase === 'connecting'}
          stats={stats}
          sparklines={sparklines}
          statsOpen={statsOpen}
          fullscreen={fullscreen}
          callbacks={{
            onTogglePlay: togglePlayback,
            onToggleMute: toggleMute,
            onVolumeChange: setPlayerVolume,
            onGoLive: goLive,
            onTransportChange,
            onQualityChange: quality => {
              if (quality === 'auto') {
                setSelectedVariant(null);
                return;
              }
              const parsed = Number(quality);
              setSelectedVariant(Number.isFinite(parsed) ? parsed : null);
            },
            onToggleLatency: toggleLatency,
            onToggleStats: () => setStatsOpen(open => !open),
            onTogglePip: togglePip,
            onToggleFullscreen: toggleFullScreen,
          }}
        />
      </div>
    </div>
  );
};
