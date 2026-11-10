import { FC, useCallback, useEffect, useRef, useState } from 'react';
import classNames from 'classnames';
import CaretRightOutlined from '@ant-design/icons/CaretRightOutlined';
import PauseOutlined from '@ant-design/icons/PauseOutlined';
import SoundFilled from '@ant-design/icons/SoundFilled';
import MutedFilled from '@ant-design/icons/MutedFilled';
import SettingOutlined from '@ant-design/icons/SettingOutlined';
import FullscreenOutlined from '@ant-design/icons/FullscreenOutlined';
import FullscreenExitOutlined from '@ant-design/icons/FullscreenExitOutlined';
import CheckOutlined from '@ant-design/icons/CheckOutlined';

import { StatsPanel } from './StatsPanel';
import { VolumeSlider } from './VolumeSlider';
import { StatsSnapshot, SparklineState, TransportPreference, PlayerQuality } from './types';
import styles from './PlayerControls.module.scss';

// The provided player autohides after 2s of inactivity.
const IDLE_HIDE_MS = 2000;

export interface PlayerControlsCallbacks {
  onTogglePlay: () => void;
  onToggleMute: () => void;
  onVolumeChange: (volume: number) => void;
  onGoLive: () => void;
  onTransportChange: (transport: TransportPreference) => void;
  onQualityChange: (quality: string) => void;
  onToggleLatency: () => void;
  onToggleStats: () => void;
  onTogglePip: () => void;
  onToggleFullscreen: () => void;
}

export type PlayerControlsProps = {
  playing: boolean;
  muted: boolean;
  volume: number;
  // Viewer preference (Auto/WebRTC/HLS) and the transport currently playing.
  transport: TransportPreference;
  activeTransport: 'webrtc' | 'hls';
  webrtcAvailable: boolean;
  qualities: PlayerQuality[];
  selectedQuality: string;
  latencyEnabled: boolean;
  latencySupported?: boolean;
  liveEdge: boolean;
  behindLiveSeconds?: number | null;
  loading?: boolean;
  stats: StatsSnapshot;
  sparklines: SparklineState;
  statsOpen: boolean;
  fullscreen?: boolean;
  // Quality rows only exist for the HLS transport in the reference player.
  showQuality?: boolean;
  // Storybook/tests only: open one of the menus on first render.
  initialMenu?: 'settings' | 'transport' | 'volume' | null;
  callbacks: PlayerControlsCallbacks;
};

// Overlay control bar ported from the provided Owncast binary. It is purely
// presentational: every action is routed to the active playback engine through
// callbacks so HLS (Video.js) and WebRTC (WHEP) share the exact same UI.
export const PlayerControls: FC<PlayerControlsProps> = ({
  playing,
  muted,
  volume,
  transport,
  activeTransport,
  webrtcAvailable,
  qualities,
  selectedQuality,
  latencyEnabled,
  latencySupported = true,
  liveEdge,
  behindLiveSeconds = null,
  loading = false,
  stats,
  sparklines,
  statsOpen,
  fullscreen = false,
  showQuality = true,
  initialMenu = null,
  callbacks,
}) => {
  const rootRef = useRef<HTMLDivElement | null>(null);
  // True while the last interaction came from a pointer, so focus that a
  // click leaves behind does not pin the control bar.
  const pointerInteractionRef = useRef(false);
  const idleTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [visible, setVisible] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(initialMenu === 'settings');
  const [transportOpen, setTransportOpen] = useState(initialMenu === 'transport');
  const [volumeVisible, setVolumeVisible] = useState(initialMenu === 'volume');

  const anyMenuOpen = settingsOpen || transportOpen || volumeVisible || statsOpen;

  const scheduleHide = useCallback(() => {
    if (idleTimer.current) clearTimeout(idleTimer.current);
    idleTimer.current = setTimeout(() => {
      // Keep the bar visible while paused, while the pointer is over any
      // player control, or while focus is inside the player (reference
      // behavior). The refs used previously were only cleared by events on
      // the overlay root, whose pointer-events are disabled, so one bar
      // interaction could pin the controls visible forever.
      const root = rootRef.current;
      // Focus only pins the bar for keyboard users: after a mouse click the
      // clicked button keeps focus, which used to keep the bar visible forever.
      const focusedInside =
        !pointerInteractionRef.current && Boolean(root && root.contains(document.activeElement));
      const pointerInside = Boolean(root && root.querySelector(':hover'));
      if (!playing || focusedInside || pointerInside) {
        scheduleHide();
        return;
      }
      setVisible(false);
    }, IDLE_HIDE_MS);
  }, [playing]);

  const reveal = useCallback(() => {
    setVisible(true);
    if (playing) scheduleHide();
  }, [playing, scheduleHide]);

  useEffect(() => {
    if (!playing || anyMenuOpen) {
      setVisible(true);
      if (idleTimer.current) clearTimeout(idleTimer.current);
      return undefined;
    }
    scheduleHide();
    return () => {
      if (idleTimer.current) clearTimeout(idleTimer.current);
    };
  }, [playing, anyMenuOpen, scheduleHide]);

  useEffect(() => {
    const onActivity = () => {
      if (!statsOpen) reveal();
    };
    // Keyboard activity anywhere still reveals the controls (shortcuts are
    // global), but pointer movement must not: the overlay handles its own
    // pointer events, and a window listener re-showed the bar from anywhere on
    // the page.
    const events: Array<[string, EventListener]> = [['keydown', onActivity]];
    events.forEach(([name, handler]) => window.addEventListener(name, handler, { passive: true }));
    return () => {
      events.forEach(([name, handler]) => window.removeEventListener(name, handler));
      if (idleTimer.current) clearTimeout(idleTimer.current);
    };
  }, [reveal, statsOpen]);

  // Close menus on outside pointerdown.
  useEffect(() => {
    if (!settingsOpen && !transportOpen) return undefined;
    const handler = (event: Event) => {
      const target = event.target as Node;
      if (rootRef.current && !rootRef.current.contains(target)) {
        setSettingsOpen(false);
        setTransportOpen(false);
      }
    };
    document.addEventListener('pointerdown', handler, true);
    return () => document.removeEventListener('pointerdown', handler, true);
  }, [settingsOpen, transportOpen]);

  const transportLabel = transport === 'auto' ? 'Auto' : transport === 'webrtc' ? 'WebRTC' : 'HLS';
  const liveTitle = liveEdge
    ? activeTransport === 'webrtc'
      ? 'Live via WebRTC'
      : 'Playing live'
    : 'Seek to live';
  const selectTransport = (next: TransportPreference) => {
    setTransportOpen(false);
    callbacks.onTransportChange(next);
  };
  const selectQuality = (quality: string) => {
    callbacks.onQualityChange(quality);
  };

  return (
    <>
      {statsOpen && (
        <StatsPanel stats={stats} sparklines={sparklines} onClose={callbacks.onToggleStats} />
      )}
      <div
        ref={rootRef}
        className={classNames(styles.overlay, { [styles.hidden]: !visible && !anyMenuOpen })}
        data-player-control="true"
        onPointerMove={() => {
          if (!anyMenuOpen) reveal();
        }}
        onPointerEnter={reveal}
        onPointerLeave={() => {
          // The bar fades out without a mouseleave on the volume group, which
          // used to leave the slider expanded after the controls disappeared.
          setVolumeVisible(false);
          if (playing && !anyMenuOpen) scheduleHide();
        }}
        onFocusCapture={reveal}
        onPointerDownCapture={() => {
          pointerInteractionRef.current = true;
        }}
        onKeyDownCapture={() => {
          pointerInteractionRef.current = false;
        }}
        // A mouse click leaves focus on the button, which kept the bar visible
        // forever. detail > 0 means a pointer click; keyboard focus is kept.
        onClickCapture={event => {
          if (event.detail > 0) {
            (event.target as HTMLElement).blur();
          }
        }}
      >
        {loading && <div className={styles.loadingSpinner} aria-label="Loading" />}
        {!playing && !loading && (
          <button
            type="button"
            className={styles.centerPlay}
            aria-label="Play"
            data-player-control="true"
            onClick={callbacks.onTogglePlay}
          >
            <CaretRightOutlined />
          </button>
        )}

        <div className={styles.controlBar} data-player-control="true">
          <button
            type="button"
            className={styles.iconButton}
            aria-label={playing ? 'Pause' : 'Play'}
            data-player-control="true"
            onClick={callbacks.onTogglePlay}
          >
            {playing ? <PauseOutlined /> : <CaretRightOutlined />}
          </button>

          <div
            className={classNames(styles.volumeGroup, { [styles.volumeVisible]: volumeVisible })}
            onMouseEnter={() => setVolumeVisible(true)}
            onMouseLeave={() => setVolumeVisible(false)}
          >
            <button
              type="button"
              className={styles.iconButton}
              aria-label={muted ? 'Unmute' : 'Mute'}
              data-player-control="true"
              onClick={callbacks.onToggleMute}
            >
              {muted || volume === 0 ? <MutedFilled /> : <SoundFilled />}
            </button>
            {volumeVisible && (
              <div className={classNames(styles.volumePopover, styles.volumeVisible)}>
                <VolumeSlider
                  value={muted ? 0 : volume}
                  onChange={callbacks.onVolumeChange}
                  onFocusChange={focused => setVolumeVisible(focused)}
                />
              </div>
            )}
          </div>

          <button
            type="button"
            className={classNames(styles.liveButton, { [styles.liveEdge]: liveEdge })}
            aria-label={liveEdge ? 'Live' : 'Seek to live'}
            title={liveTitle}
            data-behind-live={behindLiveSeconds !== null ? 'true' : undefined}
            data-player-control="true"
            onClick={() => {
              if (!liveEdge) callbacks.onGoLive();
            }}
          >
            <span className={styles.liveDot} />
            <span>LIVE</span>
          </button>

          <div className={styles.spacer} />

          <button
            type="button"
            className={styles.transportButton}
            aria-label="Playback transport"
            aria-expanded={transportOpen}
            data-player-control="true"
            onClick={() => {
              setTransportOpen(open => !open);
              setSettingsOpen(false);
            }}
          >
            {transportLabel}
          </button>

          <button
            type="button"
            className={classNames(styles.iconButton, { [styles.active]: settingsOpen })}
            aria-label="Settings"
            aria-expanded={settingsOpen}
            data-player-control="true"
            onClick={() => {
              setSettingsOpen(open => !open);
              setTransportOpen(false);
            }}
          >
            <SettingOutlined />
          </button>

          {typeof document !== 'undefined' && document.pictureInPictureEnabled && (
            <button
              type="button"
              className={styles.iconButton}
              aria-label="Picture in picture"
              data-player-control="true"
              onClick={callbacks.onTogglePip}
            >
              <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
                <path d="M4 5h16v14H4V5zm2 2v10h12V7H6zm6 4h6v5h-6v-5z" fill="currentColor" />
              </svg>
            </button>
          )}

          <button
            type="button"
            className={styles.iconButton}
            aria-label={fullscreen ? 'Exit fullscreen' : 'Fullscreen'}
            data-player-control="true"
            onClick={callbacks.onToggleFullscreen}
          >
            {fullscreen ? <FullscreenExitOutlined /> : <FullscreenOutlined />}
          </button>
        </div>

        {transportOpen && (
          <div className={styles.transportMenu} data-player-control="true">
            {[
              { value: 'auto' as TransportPreference, label: 'Auto' },
              { value: 'webrtc' as TransportPreference, label: 'WebRTC' },
              { value: 'hls' as TransportPreference, label: 'HLS' },
            ].map(item => {
              const disabled = item.value === 'webrtc' && !webrtcAvailable;
              return (
                <button
                  key={item.value}
                  type="button"
                  className={classNames(styles.menuRow, {
                    [styles.selected]: transport === item.value,
                    [styles.menuRowDisabled]: disabled,
                  })}
                  disabled={disabled}
                  data-player-control="true"
                  onClick={() => selectTransport(item.value)}
                >
                  <span>{item.label}</span>
                  {transport === item.value && <CheckOutlined />}
                </button>
              );
            })}
          </div>
        )}

        {settingsOpen && (
          <div className={styles.settingsMenu} data-player-control="true">
            {showQuality && qualities.length > 0 && (
              <>
                <div className={styles.menuHeading}>QUALITY</div>
                <button
                  type="button"
                  className={classNames(styles.menuRow, {
                    [styles.selected]: selectedQuality === 'auto',
                  })}
                  data-player-control="true"
                  onClick={() => selectQuality('auto')}
                >
                  <span>Auto</span>
                  {selectedQuality === 'auto' && <CheckOutlined />}
                </button>
                {qualities.map(quality => (
                  <button
                    key={quality.id}
                    type="button"
                    className={classNames(styles.menuRow, {
                      [styles.selected]: selectedQuality === quality.id,
                    })}
                    data-player-control="true"
                    onClick={() => selectQuality(quality.id)}
                  >
                    <span>{quality.name}</span>
                    {selectedQuality === quality.id && <CheckOutlined />}
                  </button>
                ))}
                <div className={styles.menuDivider} />
              </>
            )}

            <button
              type="button"
              className={styles.menuRow}
              aria-pressed={statsOpen}
              data-player-control="true"
              onClick={() => {
                // The provided player closes the settings menu when the
                // stats overlay is toggled.
                callbacks.onToggleStats();
                setSettingsOpen(false);
              }}
            >
              <span>Stats for nerds</span>
              <span
                className={classNames(styles.togglePill, { [styles.toggleOn]: statsOpen })}
                data-testid="stats-toggle-pill"
              >
                {statsOpen ? 'ON' : 'OFF'}
              </span>
            </button>

            <button
              type="button"
              className={styles.menuRow}
              aria-pressed={latencyEnabled}
              data-player-control="true"
              onClick={callbacks.onToggleLatency}
            >
              <span>Minimize latency</span>
              <span
                className={classNames(styles.togglePill, { [styles.toggleOn]: latencyEnabled })}
                data-testid="latency-toggle-pill"
              >
                {latencyEnabled ? 'ON' : 'OFF'}
              </span>
            </button>

            {!latencySupported && (
              <div className={styles.menuNote}>
                Minimize latency is not supported for this transport.
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
};

export default PlayerControls;
