import { clientLocationText } from '../utils/location';

describe('clientLocationText', () => {
  test('drops the N/A placeholders so a LAN viewer reads as Local', () => {
    expect(clientLocationText({ countryCode: 'N/A', regionName: 'Local' })).toBe('Local');
  });

  test('joins city, region and country', () => {
    expect(clientLocationText({ city: 'Sapporo', regionName: 'Hokkaido', countryCode: 'JP' })).toBe(
      'Sapporo, Hokkaido, JP',
    );
  });

  test('returns null when nothing is known', () => {
    expect(clientLocationText(null)).toBeNull();
    expect(clientLocationText(undefined)).toBeNull();
    expect(clientLocationText({ countryCode: 'N/A', regionName: 'Unknown' })).toBeNull();
  });
});
