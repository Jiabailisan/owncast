// Serialization helpers for the additive per-variant delivery mapping
// (master spec 4.4, models.VariantOutputs).
//
// The contract is additive: a variant without an "outputs" key keeps the legacy
// behavior (HLS enabled, WebRTC disabled). The editor must therefore omit empty
// mappings instead of sending default-valued blocks, and must preserve every
// field it does not edit (including per-protocol overrides) when saving.

import {
  ProtocolOutputSettings,
  VariantCopyFallback,
  VariantOutputMode,
  VariantOutputs,
  VideoVariant,
} from '../types/config-section';

export type VariantOutputDraft = {
  webrtcEnabled: boolean;
  webrtcMode: VariantOutputMode;
  copyFallback: VariantCopyFallback;
};

export const DEFAULT_VARIANT_OUTPUT_DRAFT: VariantOutputDraft = {
  webrtcEnabled: false,
  webrtcMode: 'inherit',
  copyFallback: 'transcode',
};

// Read the draft control values for one variant. Legacy variants have no
// outputs block: HLS stays implicitly enabled and WebRTC stays disabled.
export function extractVariantOutputDraft(outputs?: VariantOutputs): VariantOutputDraft {
  const webrtc = outputs?.webrtc;
  return {
    webrtcEnabled: webrtc?.enabled === true,
    webrtcMode: webrtc?.mode || 'inherit',
    copyFallback: webrtc?.copyFallback || 'transcode',
  };
}

// Merge the draft control values back into the stored mapping.
//
// - untouched legacy variant => undefined (the "outputs" key is omitted);
// - existing HLS block and all unknown/per-protocol override fields are kept;
// - disabling a previously enabled WebRTC mapping writes enabled:false (so the
//   server persists the change) instead of dropping the block;
// - mode "inherit" and non-copy fallbacks are omitted so the JSON stays minimal
//   and lets the server defaults apply.
export function buildVariantOutputsFromDraft(
  existing: VariantOutputs | undefined,
  draft: VariantOutputDraft,
): VariantOutputs | undefined {
  const next: VariantOutputs = {};

  if (existing?.hls) {
    next.hls = { ...existing.hls };
  }

  const existingWebRTC = existing?.webrtc;
  if (draft.webrtcEnabled) {
    const webrtc: ProtocolOutputSettings = { ...(existingWebRTC || {}), enabled: true };
    if (draft.webrtcMode === 'inherit') {
      delete webrtc.mode;
    } else {
      webrtc.mode = draft.webrtcMode;
    }
    if (draft.webrtcMode === 'copy') {
      webrtc.copyFallback = draft.copyFallback;
    } else {
      delete webrtc.copyFallback;
    }
    next.webrtc = webrtc;
  } else if (existingWebRTC) {
    next.webrtc = { ...existingWebRTC, enabled: false };
  }

  return next.hls || next.webrtc ? next : undefined;
}

// Convenience readers used by the variants table.
export function webRTCEnabledForVariant(variant: VideoVariant): boolean {
  return variant.outputs?.webrtc?.enabled === true;
}

export function webRTCOutputModeForVariant(variant: VideoVariant): VariantOutputMode {
  return variant.outputs?.webrtc?.mode || 'inherit';
}

// Strip the client-only table key and any undefined values inside outputs
// before posting the full variant list. Existing fields are preserved.
export function serializeVariantsForSave(variants: VideoVariant[]): VideoVariant[] {
  return variants.map(variant => {
    // The table adds a client-only row key; it must never reach the API.
    const cleaned: VideoVariant = { ...variant };
    delete cleaned.key;

    if (!cleaned.outputs) {
      return cleaned;
    }
    // JSON round-trip drops undefined inner fields so the payload only carries
    // values the operator actually set.
    cleaned.outputs = JSON.parse(JSON.stringify(cleaned.outputs)) as VariantOutputs;
    return cleaned;
  });
}
