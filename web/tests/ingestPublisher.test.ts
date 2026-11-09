import { formatIngestPublisher } from '../utils/format';

describe('formatIngestPublisher', () => {
  test('labels SRT ingests by publisher, never "Unknown encoder"', () => {
    expect(formatIngestPublisher('srt', '', '192.168.50.7:54321')).toBe(
      'SRT publisher 192.168.50.7',
    );
    expect(formatIngestPublisher('srt', undefined, '')).toBe('SRT publisher');
    // Even if a stale encoder value exists, SRT is labelled as a publisher.
    expect(formatIngestPublisher('srt', 'Unknown encoder', '10.0.0.2:1')).toBe(
      'SRT publisher 10.0.0.2',
    );
  });

  test('keeps a known encoder name for RTMP', () => {
    expect(formatIngestPublisher('rtmp', 'obs-output module', '203.0.113.4:5555')).toBe(
      'obs-output module 203.0.113.4',
    );
    expect(formatIngestPublisher('rtmp', '', '203.0.113.4:5555')).toBe(
      'RTMP publisher 203.0.113.4',
    );
  });
});
