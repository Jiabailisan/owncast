import { viewerLocationText } from '../components/admin/live/LiveViewerTable';
import { LiveViewer } from '../interfaces/live-status.model';

const base = { clientID: 'c', ipAddress: '192.168.50.7' } as LiveViewer;

describe('viewerLocationText', () => {
  test('renders the Local label without appending the N/A country placeholder', () => {
    expect(viewerLocationText({ ...base, region: 'Local', country: 'N/A' })).toBe('Local');
  });

  test('joins real city, region and country values', () => {
    expect(
      viewerLocationText({ ...base, city: 'San Francisco', region: 'California', country: 'US' }),
    ).toBe('San Francisco, California, US');
  });

  test('drops unknown placeholders in any position', () => {
    expect(
      viewerLocationText({ ...base, city: 'Unknown', region: 'Hokkaido', country: 'N/A' }),
    ).toBe('Hokkaido');
    expect(viewerLocationText({ ...base, region: '  ', country: 'N/A' })).toBeNull();
  });

  test('returns null when no location is known', () => {
    expect(viewerLocationText(base)).toBeNull();
    expect(viewerLocationText({ ...base, country: 'N/A' })).toBeNull();
  });
});
