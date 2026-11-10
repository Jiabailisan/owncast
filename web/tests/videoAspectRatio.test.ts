import { videoAspectRatio } from '../components/video/PlayerControls/stats';

describe('videoAspectRatio', () => {
  test('follows the decoded picture', () => {
    expect(videoAspectRatio(1920, 1080)).toBe('1.778');
    expect(videoAspectRatio(2560, 1440)).toBe('1.778');
    expect(videoAspectRatio(1440, 1080)).toBe('1.333');
    expect(videoAspectRatio(1080, 1920)).toBe('0.563');
  });

  test('falls back before metadata is available', () => {
    expect(videoAspectRatio(0, 0)).toBe('16 / 9');
    expect(videoAspectRatio(null, 1080)).toBe('16 / 9');
    expect(videoAspectRatio(undefined, undefined)).toBe('16 / 9');
    expect(videoAspectRatio(Number.NaN, 1080)).toBe('16 / 9');
  });

  test('ignores implausible ratios instead of collapsing the player', () => {
    expect(videoAspectRatio(4000, 100)).toBe('16 / 9');
    expect(videoAspectRatio(10, 1000)).toBe('16 / 9');
  });
});
