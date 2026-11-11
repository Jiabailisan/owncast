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
  liveEdge: boolean;
  loading?: boolean;
  stats: StatsSnapshot;
  sparklines: SparklineState;
  statsOpen: boolean;
  fullscreen?: boolean;
  // Quality rows only exist for the HLS transport in the reference player.
  // Storybook/tests only: open one of the menus on first render.
  initialMenu?: 'settings' | 'transport' | null;
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
  liveEdge,
  loading = false,
  stats,
  sparklines,
  statsOpen,
  fullscreen = false,
  initialMenu = null,
  callbacks,
}) => {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const pointerInsideRef = useRef(false);
  const idleTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [visible, setVisible] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(initialMenu === 'settings');
  const [transportOpen, setTransportOpen] = useState(initialMenu === 'transport');

  // The volume slider is part of the bar now, so it must not count as an open
  // menu: doing so kept the bar pinned visible forever.
  const anyMenuOpen = settingsOpen || transportOpen || statsOpen;

  const scheduleHide = useCallback(() => {
    if (idleTimer.current) clearTimeout(idleTimer.current);
    idleTimer.current = setTimeout(() => {
      // Keep the bar visible while paused, while the pointer is over any
      // player control, or while focus is inside the player (reference
      // behavior). The refs used previously were only cleared by events on
      // the overlay root, whose pointer-events are disabled, so one bar
      // interaction could pin the controls visible forever.
      const root = rootRef.current;
      // Focus only pins the bar for keyboard users. :focus-visible is exactly
      // that question, answered by the browser, so a click that leaves focus on a
      // button no longer pins the bar and no focus is destroyed to compensate.
      const active = document.activeElement;
      const focusedInside = Boolean(
        root && active && root.contains(active) && active.matches(':focus-visible'),
      );
      const pointerInside = pointerInsideRef.current;
      if (!playing || focusedInside || pointerInside) {
        scheduleHide();
        return;
      }
      setVisible(false);
    }, IDLE_HIDE_MS);
  }, [playing]);

  // A hidden bar cannot show a menu: without this the volume popover stayed
  // expanded across a hide/show cycle, because the bar faded out under the
  // pointer and no mouseleave reached the volume group.
  useEffect(() => {
    if (!visible) {
      setSettingsOpen(false);
      setTransportOpen(false);
    }
  }, [visible]);

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

  // One positional tracker drives every piece of control state: the bar is
  // visible while the pointer is over the player, and the volume popover is
  // expanded exactly while the pointer is over the volume control. Deriving both
  // from the pointer position removes the enter/leave/click state machine that
  // could get stuck.
  useEffect(() => {
    const handleMove = (event: PointerEvent) => {
      const root = rootRef.current;
      if (!root) return;

      const rect = root.getBoundingClientRect();
      const insidePlayer =
        event.clientX >= rect.left &&
        event.clientX <= rect.right &&
        event.clientY >= rect.top &&
        event.clientY <= rect.bottom;
      pointerInsideRef.current = insidePlayer;

      if (insidePlayer && !statsOpen) reveal();
    };

    const handleKey = () => {
      if (!statsOpen) reveal();
    };

    // A tap produces pointerdown, not pointermove, so touch devices need this
    // listener or the hidden bar could never be brought back.
    document.addEventListener('pointermove', handleMove, { passive: true });
    document.addEventListener('pointerdown', handleMove, { passive: true });
    window.addEventListener('keydown', handleKey, { passive: true });
    return () => {
      document.removeEventListener('pointermove', handleMove);
      document.removeEventListener('pointerdown', handleMove);
      window.removeEventListener('keydown', handleKey);
      if (idleTimer.current) clearTimeout(idleTimer.current);
    };
  }, [reveal, statsOpen]);

  // Escape closes an open menu even when the click that opened it blurred the
  // trigger, in which case the keydown never reaches the overlay root.
  useEffect(() => {
    if (!settingsOpen && !transportOpen) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      setSettingsOpen(false);
      setTransportOpen(false);
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [settingsOpen, transportOpen]);

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
          if (playing && !anyMenuOpen) scheduleHide();
        }}
        onFocusCapture={reveal}
        onKeyDownCapture={event => {
          if (event.key === 'Escape') {
            setSettingsOpen(false);
            setTransportOpen(false);
          }
        }}
        onBlurCapture={() => {
          // Keyboard users could open a menu and Tab away, which left the bar
          // pinned with the menu open because only a pointerdown closed it.
          // A pointer click also blurs the trigger before the row runs its
          // onClick, so the close is deferred to the next task: by then the
          // click has been handled and the menu can close safely.
          window.setTimeout(() => {
            const active = document.activeElement;
            if (active && rootRef.current?.contains(active)) return;

            setSettingsOpen(false);
            setTransportOpen(false);
          }, 0);
        }}
      >
        {loading && (
          <div
            className={styles.loadingSpinner}
            role="status"
            aria-live="polite"
            aria-label="Loading"
          />
        )}
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

          <div className={styles.volumeGroup}>
            <button
              type="button"
              className={styles.iconButton}
              aria-label={muted ? 'Unmute' : 'Mute'}
              data-player-control="true"
              onClick={callbacks.onToggleMute}
            >
              {muted || volume === 0 ? <MutedFilled /> : <SoundFilled />}
            </button>
            {
              <div className={styles.volumePopover}>
                <VolumeSlider value={muted ? 0 : volume} onChange={callbacks.onVolumeChange} />
              </div>
            }
          </div>

          <button
            type="button"
            className={classNames(styles.liveButton, { [styles.liveEdge]: liveEdge })}
            aria-label={liveEdge ? 'Live' : 'Seek to live'}
            title={liveTitle}
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
            {qualities.length > 0 && (
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
          </div>
        )}
      </div>
    </>
  );
};

export default PlayerControls;
