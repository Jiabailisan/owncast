// Custom player module loader and context builder (master spec 7.3).
// Contract reference: docs/examples/custom-player/README.md.
//
// The module is loaded with a same-origin dynamic import() at runtime. The
// bundler must not try to resolve it at build time: webpack/Turbopack both
// honor the magic comments below (Turbopack support verified in Next 16.2.10
// docs). The server CSP allows same-origin modules and forbids eval/inline
// scripts, which is why a native dynamic import is the only supported path.

import {
  CUSTOM_PLAYER_ERROR,
} from '../utils/apis';
import {
  OwncastPlayerAutoplay,
  OwncastPlayerContext,
  OwncastPlayerMount,
  OwncastPlayerVariant,
} from '../interfaces/custom-player.model';
import { AutoplaySetting } from '../utils/autoplay';
import { getLocalStorage } from '../utils/localStorage';
import { WebRTCVariantInfo } from '../interfaces/client-config.model';

// Fixed serving URL (spec 7.3). The configured path is served read-only here.
export const CUSTOM_PLAYER_MODULE_URL = '/custom-player/player.js';

// Existing playback event collector; custom players can report CMCD-like
// events through ctx.reportPlayback without knowing the endpoint.
export const CMCD_COLLECTOR_URL = '/api/metrics/cmcd';

// Same key the video.js settings menu persists the latency-compensator toggle
// under; kept as a literal so services do not import a component module.
const LOW_LATENCY_STORAGE_KEY = 'latencyCompensatorEnabled';

const MAX_ERROR_MESSAGE_LENGTH = 512;

export type CustomPlayerLoader = (moduleUrl: string) => Promise<unknown>;

// Runtime module loader. Kept injectable so unit tests and Storybook never
// touch the network.
export function defaultCustomPlayerLoader(moduleUrl: string): Promise<unknown> {
  return import(/* webpackIgnore: true */ /* turbopackIgnore: true */ moduleUrl);
}

// Validate the module shape and return the mount function. Anything that does
// not default-export a function is a contract violation: the viewer reports it
// and falls back to the default player.
export async function loadCustomPlayerModule(
  moduleUrl: string = CUSTOM_PLAYER_MODULE_URL,
  loader: CustomPlayerLoader = defaultCustomPlayerLoader,
): Promise<OwncastPlayerMount> {
  let loaded: unknown;
  try {
    loaded = await loader(moduleUrl);
  } catch (error) {
    throw new Error(
      'Unable to load the custom player module: ' + ((error as Error)?.message || 'unknown error'),
    );
  }

  const candidate =
    loaded && typeof loaded === 'object' && 'default' in (loaded as Record<string, unknown>)
      ? (loaded as { default?: unknown }).default
      : loaded;

  if (typeof candidate !== 'function') {
    throw new Error(
      'The custom player module must default-export a mount(el, context) function.',
    );
  }
  return candidate as OwncastPlayerMount;
}

// Map the Owncast autoplay setting onto the stable custom-player contract.
//
// Contract gap (recorded in the Phase 0 analysis): 'unmuted-when-allowed' means
// "attempt unmuted playback", so both 'always' and 'sound-only' map to it; the
// module is responsible for the muted fallback that 'always' implies. Embeds
// that explicitly request a muted start map to 'muted'.
export function mapAutoplayForCustomPlayer(
  setting: AutoplaySetting | string,
  initiallyMuted = false,
): OwncastPlayerAutoplay {
  if (initiallyMuted) return 'muted';
  if (setting === AutoplaySetting.Off) return 'none';
  return 'unmuted-when-allowed';
}

export function mapVariantsForCustomPlayer(
  variants?: WebRTCVariantInfo[],
): OwncastPlayerVariant[] {
  return (variants || []).map(variant => ({
    index: variant.index,
    name: variant.name,
    ...(variant.width !== undefined ? { width: variant.width } : {}),
    ...(variant.height !== undefined ? { height: variant.height } : {}),
  }));
}

export interface BuildCustomPlayerContextArgs {
  hlsUrl: string;
  whepUrl?: string;
  variants?: WebRTCVariantInfo[];
  isOnline: boolean;
  autoplay?: AutoplaySetting | string;
  initiallyMuted?: boolean;
  lowLatency?: boolean;
  reportPlayback?: (event: unknown) => void;
}

// Build the context object handed to the configured module.
export function buildCustomPlayerContext(
  args: BuildCustomPlayerContextArgs,
): OwncastPlayerContext {
  const {
    hlsUrl,
    whepUrl,
    variants,
    isOnline,
    autoplay = AutoplaySetting.Off,
    initiallyMuted = false,
    reportPlayback,
  } = args;

  let lowLatency = args.lowLatency;
  if (lowLatency === undefined) {
    try {
      lowLatency = getLocalStorage(LOW_LATENCY_STORAGE_KEY) === 'true';
    } catch {
      lowLatency = false;
    }
  }

  return {
    hlsUrl,
    ...(whepUrl ? { whepUrl } : {}),
    variants: mapVariantsForCustomPlayer(variants),
    isOnline,
    autoplay: mapAutoplayForCustomPlayer(autoplay, initiallyMuted),
    lowLatency,
    ...(reportPlayback ? { reportPlayback } : {}),
  };
}

export interface ReportOptions {
  fetchImpl?: typeof fetch;
}

// Report a viewer-side module failure to the admin Appearance page. Public,
// rate-limited and best-effort: never throw into playback.
export async function reportCustomPlayerError(
  message: string,
  options: ReportOptions = {},
): Promise<void> {
  const { fetchImpl = fetch } = options;
  const trimmed = (message || 'unknown error').slice(0, MAX_ERROR_MESSAGE_LENGTH);
  try {
    await fetchImpl(CUSTOM_PLAYER_ERROR, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message: trimmed }),
      keepalive: true,
    });
  } catch {
    // Reporting is best-effort; the fallback already happened.
  }
}

// Forward a module playback event to the existing CMCD collector. Best-effort.
export async function reportCustomPlayerPlaybackEvent(
  event: unknown,
  options: ReportOptions = {},
): Promise<void> {
  const { fetchImpl = fetch } = options;
  try {
    await fetchImpl(CMCD_COLLECTOR_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(event ?? {}),
      keepalive: true,
    });
  } catch {
    // Playback telemetry must never break playback.
  }
}
