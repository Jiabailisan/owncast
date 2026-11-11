// clientLocationText renders the GeoIP details the server attaches to a chat
// client, dropping the N/A and Unknown placeholders so a LAN viewer reads as
// "Local" and an unknown one renders as null (the caller shows its own fallback).
export interface ClientGeoLike {
  countryCode?: string;
  regionName?: string;
  city?: string;
}

export function clientLocationText(geo?: ClientGeoLike | null): string | null {
  if (!geo) return null;

  const placeholders = new Set(['n/a', 'unknown', '']);
  const parts = [geo.city, geo.regionName, geo.countryCode]
    .map(part => (part || '').trim())
    .filter(part => !placeholders.has(part.toLowerCase()));

  return parts.length > 0 ? parts.join(', ') : null;
}
