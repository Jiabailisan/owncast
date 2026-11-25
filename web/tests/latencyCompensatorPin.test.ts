import LatencyCompensator from '../components/video/latencyCompensator';

function makePlayerWithRepresentations(
  reps: Array<{ bandwidth: number; playlist: { disabled?: boolean } }>,
) {
  return {
    on: jest.fn(),
    off: jest.fn(),
    paused: () => false,
    seeking: () => false,
    networkState: () => 2,
    currentTime: () => 0,
    playbackRate: jest.fn(() => 1),
    buffered: () => ({ length: 0 }),
    tech: () => ({
      vhs: {
        representations: () => reps,
        playlists: {
          media: () => ({ attributes: { BANDWIDTH: reps[0].bandwidth } }),
        },
      },
    }),
  };
}

describe('LatencyCompensator quality pinning vs manual quality lock', () => {
  test('does not downgrade or unlock when the viewer manually locked one rendition', () => {
    const reps = [
      { bandwidth: 6_000_000, playlist: { disabled: undefined as boolean | undefined } },
      { bandwidth: 1_500_000, playlist: { disabled: true as boolean | undefined } },
    ];
    const player = makePlayerWithRepresentations(reps);
    const compensator = new LatencyCompensator(player);

    compensator.enableOnlyLowQualityPlayback();
    // High-bitrate manual lock must stay untouched.
    expect(reps[0].playlist.disabled).not.toBe(true);
    expect(reps[1].playlist.disabled).toBe(true);

    compensator.enableAllQualityPlayback();
    // Stopping compensation must NOT re-enable the disabled rendition.
    expect(reps[0].playlist.disabled).not.toBe(true);
    expect(reps[1].playlist.disabled).toBe(true);
  });

  test('pins to lowest rendition in Auto mode and restores all renditions on stop', () => {
    const reps = [
      { bandwidth: 6_000_000, playlist: { disabled: undefined as boolean | undefined } },
      { bandwidth: 1_500_000, playlist: { disabled: undefined as boolean | undefined } },
    ];
    const player = makePlayerWithRepresentations(reps);
    const compensator = new LatencyCompensator(player);

    compensator.enableOnlyLowQualityPlayback();
    expect(reps[0].playlist.disabled).toBe(true);
    expect(reps[1].playlist.disabled).not.toBe(true);

    // Calling pin a second time while already pinned must preserve the snapshot.
    compensator.enableOnlyLowQualityPlayback();
    expect(reps[0].playlist.disabled).toBe(true);
    expect(reps[1].playlist.disabled).not.toBe(true);

    compensator.enableAllQualityPlayback();
    expect(reps[0].playlist.disabled).not.toBe(true);
    expect(reps[1].playlist.disabled).not.toBe(true);
  });
});
