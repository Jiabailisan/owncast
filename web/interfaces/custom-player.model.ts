// Stable custom player module contract (master spec 7.3).
// Reference: docs/examples/custom-player/README.md.
//
// The viewer loads the configured file as a same-origin ES module from
// /custom-player/player.js and calls its default export in place of the
// default player. Everything else on the page stays untouched.

export interface OwncastPlayerVariant {
  index: number;
  name: string;
  width?: number;
  height?: number;
}

export type OwncastPlayerAutoplay = 'none' | 'muted' | 'unmuted-when-allowed';

// Host-provided libraries the module may use instead of relying on bare imports
// (the module is served outside the app bundle). video.js includes its HTTP
// streaming (VHS/HLS) engine, which is how Chromium plays HLS; hls.js is not
// bundled by Owncast today and is only present when a future host adds it.
export interface OwncastPlayerHlsConstructor {
  isSupported: () => boolean;
  new (config?: unknown): unknown;
}

export interface OwncastPlayerLibs {
  videojs?: unknown;
  hls?: OwncastPlayerHlsConstructor;
}

export interface OwncastPlayerContext {
  hlsUrl: string;
  whepUrl?: string;
  variants: OwncastPlayerVariant[];
  isOnline: boolean;
  autoplay: OwncastPlayerAutoplay;
  lowLatency: boolean;
  // Transport helpers provided by the host page (additive; may be empty when
  // the module is loaded outside the viewer).
  libs?: OwncastPlayerLibs;
  reportPlayback?: (event: unknown) => void;
}

export interface OwncastPlayerInstance {
  destroy: () => void;
  setVariant?: (index: number) => void;
}

export type OwncastPlayerMount = (
  el: HTMLElement,
  context: OwncastPlayerContext,
) => OwncastPlayerInstance;

export type CustomPlayerModule = {
  default?: unknown;
  mount?: unknown;
};
