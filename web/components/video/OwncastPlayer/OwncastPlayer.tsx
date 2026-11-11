/* eslint-disable max-classes-per-file */
import React, { CSSProperties, FC, useContext, useEffect, useState } from 'react';
import { useAtom, useAtomValue } from 'jotai';
import { useHotkeys } from 'react-hotkeys-hook';
import classNames from 'classnames';
import { ErrorBoundary, getErrorMessage } from 'react-error-boundary';
import { VideoJS } from '../VideoJS/VideoJS';
import { ViewerReporter, videoResolution } from '../../../utils/viewer-report';
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

// The heartbeat reports the quality the player settled on and the decoded
// resolution alongside the viewer count; both are read from this holder so the
// module-level reporter always sees the latest values.
const pingTarget = {
  quality: 'auto',
  video: null as HTMLVideoElement | null,
  measureLatency: null as (() => number | null) | null,
};
const ping = new ViewerReporter(() => ({
  protocol: 'hls' as const,
  quality: pingTarget.quality,
  // Measured per heartbeat: the playlist arrives after playback starts, so a
  // value captured once at 'playing' would stay null forever.
  latencyMs: pingTarget.measureLatency ? pingTarget.measureLatency() : null,
  resolution: videoResolution(pingTarget.video),
}));

// measureHlsLatencyMs mirrors the stats panel measurement: the playlist's
// program-date-time stamps give the real latency, the live tracker only gives the
// distance to the seekable edge.
const measureHlsLatencyMs = (player): number | null => {
  if (!player) return null;

  try {
    const vhs = player.tech({ IWillNotUseThisInPlugins: true })?.vhs;
    const playhead = Number(player.currentTime()) || 0;
    let liveEdgeLatency: number | null = null;
    const liveCurrent = Number(player.liveTracker?.liveCurrentTime?.()) || 0;
    if (liveCurrent > 0) liveEdgeLatency = Math.max(0, liveCurrent - playhead);

    const latency = hlsLiveLatencySeconds({
      nowMs: Date.now(),
      currentTime: playhead,
      segments: hlsPlaylistSegments(vhs),
      liveEdgeLatencySeconds: liveEdgeLatency,
    });

    // A plausible live latency is seconds, not minutes: a larger value means the
    // anchor segment was stale (clock skew, a stale playlist) and would be noise
    // in the viewer table.
    const plausible = (seconds: number | null): number | null => {
      if (seconds === null || !Number.isFinite(seconds) || seconds < 0 || seconds > 120) {
        return null;
      }

      return seconds * 1000;
    };

    const measured = plausible(latency);
    if (measured !== null) {
      return measured;
    }

    // Without usable program-date-time stamps the distance to the live edge is the
    // only measurable value; report it rather than nothing.
    return plausible(liveEdgeLatency);
  } catch {
    return null;
  }
};
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
  // The variant actually playing, so "Auto" can show which rendition the
  // player settled on.
  const [activeQualityName, setActiveQualityName] = useState('');
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
    pingTarget.quality = qualities.find(item => String(item.index) === qualityId)?.name ?? 'auto';
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
        // The rendition name only: the operator asked for the real quality, not a
        // wrapper around the word auto.
        qualityName: (() => {
          if (selectedQuality !== 'auto') {
            return (
              qualities.find(item => String(item.index) === selectedQuality)?.name ||
              selectedQuality
            );
          }

          // Resolve here rather than reading state: this builder is created once per
          // stats session, so a state value captured at setup would stay empty.
          const level = typeof vhs?.currentLevel === 'number' ? vhs.currentLevel : -1;
          const activeName =
            level >= 0
              ? qualities[level]?.name || vhs?.representations?.()?.[level]?.name || ''
              : '';

          return activeName ? `Auto (${activeName})` : 'Auto';
        })(),
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
        // Auto means "best quality first, step down only when the link cannot keep
        // up": start with an optimistic estimate and leave ABR enabled so it falls
        // back on real measurements. enableLowInitialPlaylist does the opposite and
        // starts every viewer on the cheapest rendition.
        enableLowInitialPlaylist: false,
        startLevel: -1,
        abrEwmaDefaultEstimate: 8000000,
        experimentalBufferBasedABR: true,
        // The Network Information API reports a coarse, often pessimistic downlink
        // (Chrome said ~1.5 Mbps on a gigabit LAN), which overrides the estimate
        // above and keeps Auto on a low rendition forever.
        useNetworkInformationApi: false,
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

    pingTarget.video = (player.el()?.querySelector('video') as HTMLVideoElement | null) ?? null;

    player.on('dispose', () => {
      ping.stop();
    });

    player.on('waiting', () => setBuffering(true));
    player.on('canplay', () => setBuffering(false));
    player.on('seeked', () => setBuffering(false));
    pingTarget.measureLatency = () => measureHlsLatencyMs(player);

    // renditionNameAt resolves a VHS level to the configured variant name. hls.js
    // lists the levels in master-playlist order, which is the configured variant
    // order, so the level index is the variant index.
    const renditionNameAt = (level: number): string => {
      if (level < 0) return '';

      const byConfig = qualities[level]?.name;
      if (byConfig) return byConfig;

      const representations =
        player.tech({ IWillNotUseThisInPlugins: true })?.vhs?.representations?.() ?? [];

      return representations[level]?.name || '';
    };

    const updateActiveQuality = () => {
      try {
        const vhs = player.tech({ IWillNotUseThisInPlugins: true })?.vhs;
        const level = typeof vhs?.currentLevel === 'number' ? vhs.currentLevel : -1;
        const name = renditionNameAt(level);
        setActiveQualityName(name);
        // Report the rendition that is actually playing, not the word auto.
        if (name) pingTarget.quality = name;
      } catch {
        // VHS not ready yet.
      }
    };

    // Auto means "best quality first": ask for the top rendition once, without
    // taking ABR out of the loop the way setting currentLevel would.
    const preferTopRendition = () => {
      if (selectedQuality !== 'auto') return;

      try {
        const vhs = player.tech({ IWillNotUseThisInPlugins: true })?.vhs;
        const levels = vhs?.levels ?? [];
        if (levels.length < 2) return;

        let topLevel = 0;
        let topBandwidth = -1;
        levels.forEach((level, index) => {
          const bandwidth = Number(level?.bitrate ?? level?.attributes?.BANDWIDTH ?? 0);
          if (bandwidth > topBandwidth) {
            topBandwidth = bandwidth;
            topLevel = index;
          }
        });

        if (vhs.currentLevel !== topLevel) {
          // nextLevel is a one-shot switch: ABR stays enabled and may step down.
          vhs.nextLevel = topLevel;
        }
      } catch {
        // VHS not ready yet.
      }
    };

    // ABR can switch renditions while playing, so keep the label in step.
    const activeQualityTimer = setInterval(() => {
      if (player.paused()) return;
      updateActiveQuality();
    }, 5000);

    player.on('dispose', () => clearInterval(activeQualityTimer));

    // The levels only exist once the manifest is parsed, so ask for the top
    // rendition there and again when playback starts.
    player.on('loadedmetadata', preferTopRendition);
    player.on('canplay', preferTopRendition);

    player.on('playing', () => {
      preferTopRendition();
      updateActiveQuality();
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
              activeQualityName={activeQualityName}
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
