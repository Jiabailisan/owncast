// Viewer-facing autoplay settings shared by the config model, player, and admin UI.
export enum AutoplaySetting {
  Off = 'off',
  Always = 'always',
  SoundOnly = 'sound-only',
}

const AUTOPLAY_VALUES = Object.values(AutoplaySetting);

// Video.js autoplay option values:
//   false  -> no autoplay
//   'any'  -> try to play with sound, fall back to muted if the browser blocks it
//   'play' -> play with sound when the browser allows it, otherwise stay paused
type VideoJSAutoplayMode = false | 'any' | 'play';

// Map an autoplay setting to the video.js autoplay value. A viewer who has
// signaled they would rather not autoplay (data-saver on, or reduced-motion
// preferred) gets no autoplay regardless of the configured setting.
export function autoplayModeForSetting(
  setting: string,
  preferences: {
    prefersReducedMotion?: boolean;
    saveData?: boolean;
    startsInaudible?: boolean;
  } = {},
): VideoJSAutoplayMode {
  if (preferences.prefersReducedMotion || preferences.saveData) {
    return false;
  }
  if (setting === AutoplaySetting.Always) {
    return 'any';
  }
  if (setting === AutoplaySetting.SoundOnly) {
    // "Only with sound" must never start silently. If the player would begin
    // inaudible (an embed asking to start muted, or a persisted volume of 0),
    // a play attempt can succeed silently anyway: Firefox permits autoplay of
    // muted or volume-0 media. So don't attempt autoplay at all.
    return preferences.startsInaudible ? false : 'play';
  }
  return false;
}

// Resolve the effective autoplay setting for an embed: a valid ?autoplay=
// override wins, otherwise fall back to the instance config value. Anything
// unrecognized falls back to the config value.
export function resolveAutoplaySetting(
  queryValue: string | undefined,
  configValue: AutoplaySetting,
): AutoplaySetting {
  if (queryValue && AUTOPLAY_VALUES.includes(queryValue as AutoplaySetting)) {
    return queryValue as AutoplaySetting;
  }
  return configValue;
}

export type AutoplayPreferences = {
  prefersReducedMotion?: boolean;
  saveData?: boolean;
  startsInaudible?: boolean;
};

// Read the browser-level playback preferences that gate autoplay: reduced
// motion, data-saver and whether the player would start inaudible. Kept
// separate from the player components so WebRTC playback can honor the exact
// same rules as the video.js HLS player.
export function readAutoplayPreferences(overrides: AutoplayPreferences = {}): AutoplayPreferences {
  if (typeof window === 'undefined') {
    return { ...overrides };
  }

  let prefersReducedMotion: boolean | undefined;
  if (typeof window.matchMedia === 'function') {
    prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  }

  const navConnection =
    typeof navigator !== 'undefined'
      ? (navigator as Navigator & { connection?: { saveData?: boolean } }).connection
      : undefined;

  return {
    prefersReducedMotion,
    saveData: navConnection?.saveData === true,
    ...overrides,
  };
}

// Whether a player should attempt to start playback on its own. Mirrors the
// video.js mapping: 'any' and 'play' both attempt playback, false does not.
export function shouldAttemptAutoplay(
  setting: string,
  preferences: AutoplayPreferences = {},
): boolean {
  return autoplayModeForSetting(setting, preferences) !== false;
}

// The big unmute affordance is only for playback the player muted itself (the
// autoplay muted fallback). A viewer-initiated or restored persisted mute must
// never show it, matching OwncastPlayer's video.js behavior.
export function shouldShowBigUnmuteOverlay(args: {
  mutedByAutoplay: boolean;
  paused: boolean;
  muted: boolean;
  volume: number;
}): boolean {
  const { mutedByAutoplay, paused, muted, volume } = args;
  return mutedByAutoplay && !paused && (muted || volume === 0);
}
