/* eslint-disable max-classes-per-file */
import React, { CSSProperties, FC, useContext, useEffect, useState } from 'react';
import { useAtom, useAtomValue } from 'jotai';
import { useHotkeys } from 'react-hotkeys-hook';
import classNames from 'classnames';
import { ErrorBoundary, getErrorMessage } from 'react-error-boundary';
import { VideoJS } from '../VideoJS/VideoJS';
import ViewerPing from '../viewer-ping';
import { VideoPoster } from '../VideoPoster/VideoPoster';
import { getLocalStorage, setLocalStorage } from '../../../utils/localStorage';
import { AutoplaySetting, autoplayModeForSetting } from '../../../utils/autoplay';
import { Localization } from '../../../types/localization';
import { isVideoPlayingAtom, clockSkewAtom } from '../../stores/ClientConfigStore';
import PlaybackMetrics from '../metrics/playback';
import LatencyCompensator from '../latencyCompensator';
import { VideoSettingsServiceContext } from '../../../services/video-settings-service';
import type { VideoQuality } from '../../../services/video-settings-service';
import { ComponentError } from '../../ui/ComponentError/ComponentError';
import { Translation } from '../../ui/Translation/Translation';
import { PlayerControls } from '../PlayerControls/PlayerControls';
import {
  TransportPreference,
  StatsSnapshot,
  SparklineState,
  PlayerQuality,
  makeEmptyStats,
} from '../PlayerControls/types';
import {
  formatViewportFrames,
  formatVolume,
  hlsConnectionSpeedKbps,
  hlsForwardBufferSeconds,
  hlsLiveLatencySeconds,
  hlsLiveMode,
  hlsMeasuredFps,
  hlsPlaylistFps,
  hlsPlaylistSegments,
  hlsResolution,
  hlsVideoBitrateKbps,
  pushSparkline,
  videoAspectRatio,
} from '../PlayerControls/stats';
import { LATENCY_COMPENSATION_ENABLED } from '../settings-menu';
import styles from './OwncastPlayer.module.scss';

const PLAYER_VOLUME = 'owncast_volume';

const ping = new ViewerPing();
let playbackMetrics = null;
let latencyCompensator = null;
let latencyCompensatorEnabled = false;

export type OwncastPlayerProps = {
  source: string;
  online: boolean;
  initiallyMuted?: boolean;
  autoplay?: AutoplaySetting;
  title: string;
  className?: string;
  // Overlay controls (ported from the provided binary).
  transport?: TransportPreference;
  webrtcAvailable?: boolean;
  onTransportChange?: (transport: TransportPreference) => void;
  // WHEP failure message shown briefly in the reference-style error banner.
  transportError?: string;
};

export const OwncastPlayer: FC<OwncastPlayerProps> = ({
  source,
  online,
  initiallyMuted = false,
  autoplay = AutoplaySetting.Off,
  title,
  className,
  transport = 'auto',
  webrtcAvailable = false,
  onTransportChange = () => {},
  transportError,
}) => {
  const VideoSettingsService = useContext(VideoSettingsServiceContext);
  const playerRef = React.useRef(null);
  const containerRef = React.useRef<HTMLDivElement | null>(null);
  const [videoPlaying, setVideoPlaying] = useAtom(isVideoPlayingAtom);
  const [isPlaying, setIsPlaying] = useState(false);
  const [muted, setMuted] = useState(initiallyMuted);
  const [volume, setVolume] = useState(1);
  const [liveEdge, setLiveEdge] = useState(true);
  const [qualities, setQualities] = useState<VideoQuality[]>([]);
  // HLS quality is intentionally NOT persisted (reference behavior).
  const [selectedQuality, setSelectedQuality] = useState('auto');
  const [transportErrorVisible, setTransportErrorVisible] = useState(false);
  // Seed from storage so the menu shows the saved state on the first paint,
  // exactly like the WebRTC player; the player itself applies it when ready.
  const [latencyEnabled, setLatencyEnabled] = useState(
    () => getLocalStorage(LATENCY_COMPENSATION_ENABLED) === 'true',
  );
  // The player box follows the decoded picture so a fixed container height can
  // never letterbox the video (which showed as black bars above and below).
  const [aspectRatio, setAspectRatio] = useState('16 / 9');
  const [fullscreen, setFullscreen] = useState(false);
  const [statsOpen, setStatsOpen] = useState(false);
  // video.js buffering state, surfaced through the custom overlay spinner.
  const [buffering, setBuffering] = useState(false);
  const [stats, setStats] = useState<StatsSnapshot>(() => makeEmptyStats(false));
  const [sparklines, setSparklines] = useState<SparklineState>({ video: [], audio: [] });
  // Rolling HLS metering state for the stats effect: decoded-frame deltas,
  // WebKit decoded-byte deltas and the /api/status ingest values.
  const hlsMeterRef = React.useRef<{
    frames: number | null;
    sampledAt: number;
    decodedBytes: number | null;
    decodedAt: number;
    audioBitrateKbps: number | null;
    videoPipeline: string | null;
  }>({
    frames: null,
    sampledAt: 0,
    decodedBytes: null,
    decodedAt: 0,
    audioBitrateKbps: null,
    videoPipeline: null,
  });
  const clockSkew = useAtomValue(clockSkewAtom);

  // A persisted volume of 0 is a mute the viewer chose on a previous visit
  // (handleVolume stores muted as 0). Restoring it is a manual mute, not an
  // autoplay one, so the big unmute overlay stays away.
  const savedVolume = typeof window !== 'undefined' ? getLocalStorage(PLAYER_VOLUME) : null;
  const startedMutedByViewer = savedVolume !== null && Number(savedVolume) === 0;

  const setSavedVolume = () => {
    try {
      playerRef.current.volume(getLocalStorage(PLAYER_VOLUME) || 1);
    } catch (err) {
      console.warn(err);
    }
  };

  const syncVolumeState = () => {
    const player = playerRef.current;
    if (!player) return;
    setMuted(Boolean(player.muted()) || player.volume() === 0);
    setVolume(Number(player.volume()) || 0);
  };

  const handleVolume = () => {
    setLocalStorage(PLAYER_VOLUME, playerRef.current.muted() ? 0 : playerRef.current.volume());
    syncVolumeState();
  };

  const togglePlayback = () => {
    if (playerRef.current.paused()) {
      playerRef.current.play();
    } else {
      playerRef.current.pause();
    }
  };

  const toggleMute = () => {
    if (playerRef.current.muted() || playerRef.current.volume() === 0) {
      // Unmuting restores full volume; the slider is not offered on touch devices,
      // so 100% is the only sensible default.
      playerRef.current.volume(1);
      playerRef.current.muted(false);
    } else {
      playerRef.current.volume(0);
    }
    handleVolume();
  };

  const setPlayerVolume = (next: number) => {
    playerRef.current.muted(false);
    playerRef.current.volume(Math.max(0, Math.min(1, next)));
    handleVolume();
  };

  const toggleFullScreen = () => {
    if (playerRef.current.isFullscreen()) {
      playerRef.current.exitFullscreen();
    } else {
      playerRef.current.requestFullscreen();
    }
  };

  const togglePip = () => {
    const video =
      (playerRef.current?.el?.() as HTMLElement | undefined)?.querySelector('video') || null;
    if (!video || !document.pictureInPictureEnabled) return;
    if (document.pictureInPictureElement) {
      document.exitPictureInPicture().catch(() => {});
    } else {
      (video as HTMLVideoElement).requestPictureInPicture?.().catch(() => {});
    }
  };

  const goLive = () => {
    try {
      playerRef.current?.liveTracker?.seekToLiveEdge?.();
    } catch {
      // Live tracker is unavailable on non-live sources.
    }
  };

  const applyQuality = (qualityId: string) => {
    try {
      const tech = playerRef.current?.tech?.({ IWillNotUseThisInPlugins: true });
      const representations = tech?.vhs?.representations?.();
      if (!Array.isArray(representations) || representations.length === 0) return false;
      if (qualityId === 'auto') {
        representations.forEach(rep => rep.enabled(true));
      } else {
        const index = Number(qualityId);
        representations.forEach((rep, repIndex) => rep.enabled(repIndex === index));
      }
      return true;
    } catch {
      return false;
    }
  };

  const selectQuality = (qualityId: string) => {
    setSelectedQuality(qualityId);
    applyQuality(qualityId);
  };

  const startLatencyCompensator = () => {
    if (latencyCompensator) {
      latencyCompensator.disable();
    }
    latencyCompensatorEnabled = true;
    latencyCompensator = new LatencyCompensator(playerRef.current);
    latencyCompensator.setClockSkew(clockSkew);
    latencyCompensator.enable();
    setLocalStorage(LATENCY_COMPENSATION_ENABLED, true);
  };

  const stopLatencyCompensator = () => {
    if (latencyCompensator) {
      latencyCompensator.disable();
    }
    latencyCompensator = null;
    latencyCompensatorEnabled = false;
    setLocalStorage(LATENCY_COMPENSATION_ENABLED, false);
  };

  const toggleLatency = () => {
    if (latencyCompensatorEnabled) {
      stopLatencyCompensator();
    } else {
      startLatencyCompensator();
    }
    setLatencyEnabled(latencyCompensatorEnabled);
    return latencyCompensatorEnabled;
  };

  const setupLatencyCompensator = player => {
    const tech = player.tech({ IWillNotUseThisInPlugins: true });
    if (!tech || !tech.vhs) {
      return;
    }
    const savedEnabled = getLocalStorage(LATENCY_COMPENSATION_ENABLED) === 'true';
    if (savedEnabled) {
      startLatencyCompensator();
      setLatencyEnabled(true);
    } else {
      stopLatencyCompensator();
      setLatencyEnabled(false);
    }
  };

  const setupUnmuteButton = (player, videojs) => {
    const VJSButtonClass = videojs.getComponent('Button');

    class UnmuteButton extends VJSButtonClass {
      constructor() {
        super(player);
      }

      // eslint-disable-next-line class-methods-use-this
      handleClick() {
        player.muted(false);
        if (player.volume() === 0) {
          // Unmuting restores full volume: mobile hides the volume slider, so this
          // notice is the only way to get sound back there, and a persisted 0.7
          // would leave it quiet.
          player.volume(1);
        }
      }
    }

    const unmuteButton = new UnmuteButton();
    unmuteButton.addClass('vjs-big-unmute-button');
    // Kept in English on purpose: this is an action hint, not a translated label.
    const unmuteLabel = 'Unmute';
    unmuteButton.controlText(unmuteLabel);
    const iconPlaceholder = unmuteButton.el().querySelector('.vjs-icon-placeholder');
    if (iconPlaceholder) {
      iconPlaceholder.innerHTML =
        '<svg viewBox="64 64 896 896" fill="currentColor" fill-rule="evenodd" aria-hidden="true" focusable="false"><path d="M771.91 115a31.65 31.65 0 00-17.42 5.27L400 351.97H236a16 16 0 00-16 16v288.06a16 16 0 0016 16h164l354.5 231.7a31.66 31.66 0 0017.42 5.27c16.65 0 32.08-13.25 32.08-32.06V147.06c0-18.8-15.44-32.06-32.09-32.06"></path></svg>';
    }
    // A static label next to the icon; video.js only offers a visually hidden
    // control text, which is revealed on hover and shifts the pill.
    const unmuteLabelSpan = document.createElement('span');
    unmuteLabelSpan.textContent = unmuteLabel;
    unmuteButton.el().appendChild(unmuteLabelSpan);
    player.addChild(unmuteButton);

    const inaudible = () => player.muted() || player.volume() === 0;
    // Kept for the autoplay-success handler below; the notice itself is driven by
    // the audible state now.
    let mutedByAutoplay = !startedMutedByViewer && !player.paused() && inaudible();

    const updateUnmuteButton = () => {
      if (!inaudible()) {
        mutedByAutoplay = false;
      }
      // Show the notice whenever the player is audible-off and playing, not only
      // for the first autoplay-muted case: muting again later must show it too.
      if (!player.paused() && inaudible()) {
        unmuteButton.show();
      } else {
        unmuteButton.hide();
      }
    };

    player.on('autoplay-success', () => {
      mutedByAutoplay = !startedMutedByViewer && inaudible();
      void mutedByAutoplay;
      updateUnmuteButton();
    });

    player.on(['playing', 'pause', 'ended', 'volumechange'], updateUnmuteButton);
    updateUnmuteButton();
  };

  // Live edge / behind-live tracking (HLS only; WebRTC is always live).
  useEffect(() => {
    const timer = setInterval(() => {
      const player = playerRef.current;
      if (!player?.liveTracker) return;
      try {
        const current = Number(player.currentTime()) || 0;
        const liveCurrent = Number(player.liveTracker.liveCurrentTime?.()) || 0;
        const behind = Math.max(0, liveCurrent - current);
        const atEdge = behind < 8;
        setLiveEdge(atEdge);
      } catch {
        // Live tracker may not be ready.
      }
    }, 1000);
    return () => clearInterval(timer);
  }, []);

  // Keep the player box in sync with the decoded picture.
  useEffect(() => {
    const sync = () => {
      const video =
        (playerRef.current?.el?.() as HTMLElement | undefined)?.querySelector('video') || null;
      if (!video) return;
      setAspectRatio(videoAspectRatio(video.videoWidth, video.videoHeight));
    };
    sync();
    const timer = setInterval(sync, 1000);

    return () => clearInterval(timer);
  }, []);

  // Sample HLS playback stats at 1 Hz while the Stats for nerds panel is open.
  // Measured the same way as the provided player: VHS segment bytes / loaded
  // seconds for the video bitrate, bytes / transfer duration for the
  // connection speed, decoded frame deltas (+ playlist FRAME-RATE fallback)
  // for fps, and the public /api/status ingest audio bitrate (polled while the
  // panel is open). HLS never reports RTT/loss from navigator.connection.
  useEffect(() => {
    if (!statsOpen) return undefined;
    const meter = hlsMeterRef.current;
    meter.frames = null;
    meter.sampledAt = 0;
    meter.decodedBytes = null;
    meter.decodedAt = 0;
    let disposed = false;
    let statusAbort: AbortController | null = null;

    const readStatus = () => {
      statusAbort?.abort();
      statusAbort = new AbortController();
      fetch('/api/status', { cache: 'no-store', signal: statusAbort.signal })
        .then(response => (response.ok ? response.json() : null))
        .then(data => {
          if (disposed || !data) return;
          const audio = Number(data.audioBitrate);
          meter.audioBitrateKbps = Number.isFinite(audio) && audio > 0 ? audio : null;
          meter.videoPipeline = typeof data.videoPipeline === 'string' ? data.videoPipeline : null;
        })
        .catch(() => {});
    };
    readStatus();
    const statusTimer = setInterval(readStatus, 2000);

    const build = () => {
      const player = playerRef.current;
      if (!player || disposed) return;
      const el = player.el?.() as HTMLElement | undefined;
      const video = el?.querySelector('video') as HTMLVideoElement | null;
      const tech = player.tech?.({ IWillNotUseThisInPlugins: true });
      const vhs = tech?.vhs;
      const quality = video?.getVideoPlaybackQuality?.();
      const now = typeof performance !== 'undefined' ? performance.now() : Date.now();

      const totalFrames =
        typeof quality?.totalVideoFrames === 'number' ? quality.totalVideoFrames : null;
      let measuredFps: number | null = null;
      if (totalFrames !== null && meter.frames !== null && meter.sampledAt > 0) {
        measuredFps = hlsMeasuredFps(totalFrames - meter.frames, (now - meter.sampledAt) / 1000);
      }
      if (totalFrames !== null) {
        meter.frames = totalFrames;
        meter.sampledAt = now;
      }

      // WebKit exposes a decoded-byte counter, the most direct bitrate source.
      const decodedVideo = video as
        (HTMLVideoElement & { webkitVideoDecodedByteCount?: number }) | null;
      const decodedBytes = Number(decodedVideo?.webkitVideoDecodedByteCount);
      let decodedBitsPerSecond: number | null = null;
      if (
        Number.isFinite(decodedBytes) &&
        decodedBytes > 0 &&
        meter.decodedBytes !== null &&
        meter.decodedAt > 0 &&
        now > meter.decodedAt
      ) {
        decodedBitsPerSecond =
          ((decodedBytes - meter.decodedBytes) * 8) / ((now - meter.decodedAt) / 1000);
      }
      meter.decodedBytes = Number.isFinite(decodedBytes) ? decodedBytes : null;
      meter.decodedAt = now;

      const representations =
        typeof vhs?.representations === 'function' ? vhs.representations() : [];
      const activeRepresentation = Array.isArray(representations)
        ? representations.find(representation => representation?.enabled?.())
        : null;
      const playlistFps = hlsPlaylistFps(
        activeRepresentation?.attributes ?? null,
        activeRepresentation?.name ??
          qualities.find(item => String(item.index) === selectedQuality)?.name ??
          null,
      );
      const fps = measuredFps ?? playlistFps;

      const meterInput = {
        mediaBytesTransferred: Number.isFinite(Number(vhs?.mediaBytesTransferred))
          ? Number(vhs?.mediaBytesTransferred)
          : null,
        mediaSecondsLoaded: Number.isFinite(Number(vhs?.mediaSecondsLoaded))
          ? Number(vhs?.mediaSecondsLoaded)
          : null,
        mediaTransferDurationMs: Number.isFinite(Number(vhs?.mediaTransferDuration))
          ? Number(vhs?.mediaTransferDuration)
          : null,
        systemBandwidthBps: Number.isFinite(Number(vhs?.systemBandwidth))
          ? Number(vhs?.systemBandwidth)
          : null,
        representationBandwidthBps: Number.isFinite(Number(activeRepresentation?.bandwidth))
          ? Number(activeRepresentation?.bandwidth)
          : null,
        decodedBitsPerSecond,
        audioBitrateKbps: meter.audioBitrateKbps,
      };
      const videoBitrateKbps = hlsVideoBitrateKbps(meterInput);
      const connectionSpeedKbps = hlsConnectionSpeedKbps(meterInput, videoBitrateKbps);
      const resolution = hlsResolution(video?.videoWidth, video?.videoHeight, fps);
      const audioBitrateKbps = meter.audioBitrateKbps;
      const volumeInfo = formatVolume(video?.volume ?? 1, Boolean(video?.muted));
      const bufferedRanges: { start: number; end: number }[] = [];
      if (video?.buffered) {
        for (let index = 0; index < video.buffered.length; index += 1) {
          bufferedRanges.push({
            start: video.buffered.start(index),
            end: video.buffered.end(index),
          });
        }
      }
      const currentPlayhead = Number(video?.currentTime ?? player.currentTime?.()) || 0;
      const bufferHealth = hlsForwardBufferSeconds(bufferedRanges, currentPlayhead);
      let liveEdgeLatency: number | null = null;
      try {
        const liveCurrent = Number(player.liveTracker?.liveCurrentTime?.()) || 0;
        if (liveCurrent > 0) liveEdgeLatency = Math.max(0, liveCurrent - currentPlayhead);
      } catch {
        liveEdgeLatency = null;
      }
      // Real latency comes from the playlist's program-date-time stamps; the
      // live tracker only measures the distance to the seekable edge.
      const latency = hlsLiveLatencySeconds({
        nowMs: Date.now(),
        currentTime: currentPlayhead,
        segments: hlsPlaylistSegments(vhs),
        liveEdgeLatencySeconds: liveEdgeLatency,
      });

      setStats({
        collecting: false,
        isWebRTC: false,
        streamId: source ? source.split('/').filter(Boolean).slice(-2).join('/') : 'HLS',
        viewport: formatViewportFrames(
          typeof window === 'undefined' ? 0 : window.innerWidth,
          typeof window === 'undefined' ? 0 : window.innerHeight,
          quality ? quality.droppedVideoFrames : null,
          quality ? quality.totalVideoFrames : null,
        ),
        frames: formatViewportFrames(
          typeof window === 'undefined' ? 0 : window.innerWidth,
          typeof window === 'undefined' ? 0 : window.innerHeight,
          quality ? quality.droppedVideoFrames : null,
          quality ? quality.totalVideoFrames : null,
        ),
        resolution,
        optimalResolution: resolution,
        volume: volumeInfo.volume,
        normalizedVolume: volumeInfo.normalizedVolume,
        codecs: 'H.264 / AAC (HLS)',
        sdp: '—',
        qualityName:
          selectedQuality === 'auto'
            ? 'Auto'
            : qualities.find(item => String(item.index) === selectedQuality)?.name ||
              selectedQuality,
        videoBitrateKbps,
        audioBitrateKbps,
        connectionSpeedKbps,
        rttMs: null,
        jitterMs: null,
        packetsLost: null,
        packetsReceived: null,
        bufferSeconds: bufferHealth,
        liveLatencySeconds: latency,
        liveMode: hlsLiveMode(meter.videoPipeline),
        date: new Date().toLocaleString(),
      });
      setSparklines(current => ({
        video: pushSparkline(current.video, videoBitrateKbps),
        audio: pushSparkline(current.audio, audioBitrateKbps),
      }));
    };

    build();
    const timer = setInterval(build, 1000);
    return () => {
      disposed = true;
      clearInterval(timer);
      clearInterval(statusTimer);
      statusAbort?.abort();
    };
  }, [statsOpen, selectedQuality, qualities, source]);

  // Register keyboard shortcut for the space bar to toggle playback
  useHotkeys('space', e => {
    e.preventDefault();
    togglePlayback();
  });

  useHotkeys('f', toggleFullScreen, { enableOnContentEditable: false });
  useHotkeys('m', toggleMute, { enableOnContentEditable: false });
  useHotkeys(
    'i',
    () => {
      togglePip();
    },
    { enableOnContentEditable: false },
  );
  useHotkeys('0', () => playerRef.current.volume(playerRef.current.volume() + 0.1), {
    enableOnContentEditable: false,
  });
  useHotkeys('9', () => playerRef.current.volume(playerRef.current.volume() - 0.1), {
    enableOnContentEditable: false,
  });

  const prefersReducedMotion =
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const navConnection =
    typeof navigator !== 'undefined'
      ? (navigator as Navigator & { connection?: { saveData?: boolean } }).connection
      : undefined;
  const startsInaudible = initiallyMuted || startedMutedByViewer;
  const autoplayMode = autoplayModeForSetting(autoplay, {
    prefersReducedMotion,
    saveData: navConnection?.saveData === true,
    startsInaudible,
  });

  const videoJsOptions = {
    autoplay: autoplayMode,
    controls: false,
    bigPlayButton: false,
    responsive: true,
    fluid: false,
    fill: true,
    playsinline: true,
    liveui: false,
    preload: 'auto',
    muted: initiallyMuted,
    html5: {
      vhs: {
        enableLowInitialPlaylist: true,
        experimentalBufferBasedABR: true,
        useNetworkInformationApi: true,
        maxPlaylistRetries: 30,
      },
    },
    liveTracker: {
      trackingThreshold: 0,
      liveTolerance: 15,
    },
    sources: [
      {
        src: source,
        type: 'application/x-mpegURL',
      },
    ],
  };

  const handlePlayerReady = async (player, videojs) => {
    playerRef.current = player;
    setSavedVolume();
    setupUnmuteButton(player, videojs);

    player.on('dispose', () => {
      ping.stop();
    });

    player.on('waiting', () => setBuffering(true));
    player.on('canplay', () => setBuffering(false));
    player.on('seeked', () => setBuffering(false));
    player.on('playing', () => {
      ping.start();
      setVideoPlaying(true);
      setIsPlaying(true);
      setStats(prev => ({ ...prev, collecting: true }));
    });

    player.on('pause', () => {
      ping.stop();
      setVideoPlaying(false);
      setIsPlaying(false);
    });

    player.on('ended', () => {
      ping.stop();
      setVideoPlaying(false);
      setIsPlaying(false);
    });

    player.on('volumechange', handleVolume);
    player.on('fullscreenchange', () => setFullscreen(Boolean(player.isFullscreen())));
    syncVolumeState();

    playbackMetrics = new PlaybackMetrics(player, videojs);
    playbackMetrics.setClockSkew(clockSkew);

    try {
      const fetchedQualities = await VideoSettingsService.getVideoQualities();
      setQualities(fetchedQualities);
    } catch (err) {
      console.warn('Unable to load HLS qualities', err);
    }

    setupLatencyCompensator(player);
  };

  // Reference-style transport error banner: shown for 5s after a WHEP failure
  // while the player has fallen back to HLS.
  useEffect(() => {
    if (!transportError) {
      setTransportErrorVisible(false);
      return undefined;
    }
    setTransportErrorVisible(true);
    const timer = setTimeout(() => setTransportErrorVisible(false), 5000);
    return () => clearTimeout(timer);
  }, [transportError]);

  useEffect(() => {
    if (playbackMetrics) {
      playbackMetrics.setClockSkew(clockSkew);
    }
  }, [clockSkew]);

  useEffect(
    () => () => {
      stopLatencyCompensator();
      playbackMetrics?.stop();
    },
    [],
  );

  const qualityOptions: PlayerQuality[] = qualities.map(quality => ({
    id: String(quality.index),
    name: quality.name,
  }));

  return (
    <ErrorBoundary
      // eslint-disable-next-line react/no-unstable-nested-components
      fallbackRender={({ error, resetErrorBoundary }) => (
        <ComponentError
          componentName="OwncastPlayer"
          message={getErrorMessage(error)}
          retryFunction={resetErrorBoundary}
        />
      )}
    >
      <div
        ref={containerRef}
        className={classNames(styles.container, className)}
        id="player"
        data-protocol="hls"
        style={{ '--video-aspect-ratio': aspectRatio } as CSSProperties}
      >
        {online && (
          <div className={styles.player}>
            <VideoJS options={videoJsOptions} onReady={handlePlayerReady} aria-label={title} />
            {transportErrorVisible && (
              <div className={styles.transportError} data-testid="transport-error">
                <Translation
                  translationKey={Localization.Frontend.Player.webrtcUnavailableNotice}
                  defaultText="WebRTC is unavailable; using HLS."
                />
              </div>
            )}
            <PlayerControls
              playing={isPlaying && videoPlaying}
              muted={muted}
              volume={volume}
              transport={transport}
              activeTransport="hls"
              webrtcAvailable={webrtcAvailable}
              qualities={qualityOptions}
              selectedQuality={selectedQuality}
              latencyEnabled={latencyEnabled}
              liveEdge={liveEdge}
              stats={stats}
              sparklines={sparklines}
              loading={buffering}
              statsOpen={statsOpen}
              fullscreen={fullscreen}
              callbacks={{
                onTogglePlay: togglePlayback,
                onToggleMute: toggleMute,
                onVolumeChange: setPlayerVolume,
                onGoLive: goLive,
                onTransportChange,
                onQualityChange: selectQuality,
                onToggleLatency: toggleLatency,
                onToggleStats: () => {
                  setStatsOpen(open => {
                    if (!open) setStats(prev => ({ ...prev, collecting: true }));
                    return !open;
                  });
                },
                onTogglePip: togglePip,
                onToggleFullscreen: toggleFullScreen,
              }}
            />
          </div>
        )}
        <div className={styles.poster}>
          {!videoPlaying && (
            <VideoPoster online={online} initialSrc="/thumbnail.jpg" src="/thumbnail.jpg" />
          )}
        </div>
      </div>
    </ErrorBoundary>
  );
};
