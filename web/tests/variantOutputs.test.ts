import {
  buildVariantOutputsFromDraft,
  DEFAULT_VARIANT_OUTPUT_DRAFT,
  extractVariantOutputDraft,
  serializeVariantsForSave,
  webRTCEnabledForVariant,
  webRTCOutputModeForVariant,
} from '../utils/variant-outputs';
import { VideoVariant } from '../types/config-section';

const legacyVariant: VideoVariant = {
  cpuUsageLevel: 3,
  framerate: 24,
  audioPassthrough: true,
  audioBitrate: 0,
  videoPassthrough: false,
  videoBitrate: 1200,
  scaledWidth: 1280,
  scaledHeight: 720,
  name: '720p',
};

describe('variant output mapping serialization', () => {
  test('extracts legacy defaults from a variant without an outputs block', () => {
    expect(extractVariantOutputDraft(undefined)).toEqual(DEFAULT_VARIANT_OUTPUT_DRAFT);
  });

  test('extracts an explicit WebRTC mapping', () => {
    expect(
      extractVariantOutputDraft({
        webrtc: { enabled: true, mode: 'copy', copyFallback: 'disable' },
      }),
    ).toEqual({ webrtcEnabled: true, webrtcMode: 'copy', copyFallback: 'disable' });
  });

  test('an untouched legacy variant keeps omitting the outputs field', () => {
    expect(buildVariantOutputsFromDraft(undefined, DEFAULT_VARIANT_OUTPUT_DRAFT)).toBeUndefined();
  });

  test('enabling WebRTC writes an explicit enabled mapping with inherit mode omitted', () => {
    expect(
      buildVariantOutputsFromDraft(undefined, {
        webrtcEnabled: true,
        webrtcMode: 'inherit',
        copyFallback: 'transcode',
      }),
    ).toEqual({ webrtc: { enabled: true } });
  });

  test('copy mode carries the copy fallback; transcode/inherit do not', () => {
    expect(
      buildVariantOutputsFromDraft(undefined, {
        webrtcEnabled: true,
        webrtcMode: 'copy',
        copyFallback: 'disable',
      }),
    ).toEqual({ webrtc: { enabled: true, mode: 'copy', copyFallback: 'disable' } });

    expect(
      buildVariantOutputsFromDraft(
        { webrtc: { enabled: true, mode: 'copy', copyFallback: 'transcode' } },
        { webrtcEnabled: true, webrtcMode: 'transcode', copyFallback: 'transcode' },
      ),
    ).toEqual({ webrtc: { enabled: true, mode: 'transcode' } });
  });

  test('disabling an existing mapping persists enabled:false and preserves overrides', () => {
    expect(
      buildVariantOutputsFromDraft(
        { webrtc: { enabled: true, mode: 'transcode', videoBitrate: 2500, framerate: 30 } },
        { webrtcEnabled: false, webrtcMode: 'inherit', copyFallback: 'transcode' },
      ),
    ).toEqual({ webrtc: { enabled: false, mode: 'transcode', videoBitrate: 2500, framerate: 30 } });
  });

  test('preserves an existing HLS block and per-protocol overrides', () => {
    const existing = {
      hls: { enabled: false },
      webrtc: { enabled: true, scaledWidth: 640, scaledHeight: 360 },
    };
    expect(
      buildVariantOutputsFromDraft(existing, {
        webrtcEnabled: true,
        webrtcMode: 'copy',
        copyFallback: 'transcode',
      }),
    ).toEqual({
      hls: { enabled: false },
      webrtc: {
        enabled: true,
        scaledWidth: 640,
        scaledHeight: 360,
        mode: 'copy',
        copyFallback: 'transcode',
      },
    });
  });

  test('variant readers mirror the backend helper semantics', () => {
    expect(webRTCEnabledForVariant(legacyVariant)).toBe(false);
    expect(
      webRTCEnabledForVariant({ ...legacyVariant, outputs: { webrtc: { enabled: true } } }),
    ).toBe(true);
    expect(webRTCOutputModeForVariant(legacyVariant)).toBe('inherit');
    expect(
      webRTCOutputModeForVariant({
        ...legacyVariant,
        outputs: { webrtc: { enabled: true, mode: 'copy' } },
      }),
    ).toBe('copy');
  });

  test('serializeVariantsForSave strips the client table key and undefined fields', () => {
    const variants: VideoVariant[] = [
      { ...legacyVariant, key: 1 },
      {
        ...legacyVariant,
        key: 2,
        outputs: { webrtc: { enabled: true, mode: 'transcode', copyFallback: undefined } },
      },
    ];
    const serialized = serializeVariantsForSave(variants);
    expect(serialized[0].key).toBeUndefined();
    expect(serialized[0].outputs).toBeUndefined();
    expect(serialized[1].outputs).toEqual({ webrtc: { enabled: true, mode: 'transcode' } });
    // Existing variant fields survive unchanged.
    expect(serialized[1]).toMatchObject({
      name: '720p',
      videoBitrate: 1200,
      scaledHeight: 720,
      videoPassthrough: false,
    });
  });
});
