// Settings metadata contract shared by the admin Live Streaming forms.
//
// The backend owns the single source of truth for defaults, ranges, enums and
// apply modes (docs/architecture/api-contract.md section 1). The admin form is
// rendered generically from this metadata so a setting can never be stored
// without also being described here.

export type SettingType =
  | 'bool'
  | 'int'
  | 'float'
  | 'string'
  | 'stringList'
  | 'enum'
  | 'cidrList'
  | 'secret'
  // Structured list of TURN servers (per-entry credentials). Extension of the
  // contract type list; matches config/settings.TypeTurnServerList.
  | 'turnServerList';

// How a change takes effect. Mirrors the spec's applyMode semantics so the UI
// can show a badge per field and a pending-restart banner. 'n/a' marks
// metadata-only read-only entries (fixed options); it is only used with
// readOnly fields.
export type ApplyMode = 'live' | 'restart-service' | 'restart-stream' | 'restart-server' | 'n/a';

export interface FieldMeta {
  key: string;
  // Short human-readable form label from the backend metadata registry. Older
  // metadata may omit it; the UI falls back to a humanized key.
  label?: string;
  group: string;
  type: SettingType;
  default: unknown;
  min?: number | null;
  max?: number | null;
  enum?: string[] | null;
  unit?: string | null;
  description: string;
  secret: boolean;
  applyMode: ApplyMode;
  readOnly: boolean;
}

export interface SettingsGroupMeta {
  id: string;
  label: string;
}

export interface SettingsSectionMeta {
  groups?: SettingsGroupMeta[];
  fields: FieldMeta[];
}

// GET /api/admin/config/settings-metadata. The backend always serializes all
// four sections; appearance/output stay optional here so the forms tolerate a
// server that predates them.
export interface SettingsMetadata {
  groups: SettingsGroupMeta[];
  srt: SettingsSectionMeta;
  webrtc: SettingsSectionMeta;
  appearance?: SettingsSectionMeta;
  output?: SettingsSectionMeta;
}

// {"field": "port", "message": "must not collide with the web port"}
export interface FieldError {
  field: string;
  message: string;
}

export interface PendingRestart {
  pending: boolean;
  fields: string[];
}

// Validation failures come back as a list; forms render per-field messages.
export function fieldErrorsToMap(errors?: FieldError[] | null): Record<string, string> {
  const map: Record<string, string> = {};
  (errors || []).forEach(({ field, message }) => {
    if (field) map[field] = message;
  });
  return map;
}

// Build a starting config object from metadata defaults so a form can render
// even before the GET response arrives (and for Storybook/tests).
export function defaultsFromMetadata(fields: FieldMeta[]): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  fields.forEach(field => {
    values[field.key] = field.default;
  });
  return values;
}

// Common field-key acronyms get their canonical spelling/units. This keeps
// generated labels readable without the backend having to ship a short label.
const FIELD_ACRONYMS: Record<string, string> = {
  Ms: '(ms)',
  Kbps: '(kbps)',
  Pct: '(%)',
  Id: 'ID',
  Ip: 'IP',
  Url: 'URL',
  Uri: 'URI',
  Ttl: 'TTL',
  Tos: 'TOS',
  Cidr: 'CIDR',
  Cidrs: 'CIDRs',
  Udp: 'UDP',
  Tcp: 'TCP',
  Srt: 'SRT',
  Rtmp: 'RTMP',
  Hls: 'HLS',
  Rtc: 'RTC',
  Gop: 'GOP',
  Rtt: 'RTT',
  Nack: 'NACK',
  Twcc: 'TWCC',
  Fec: 'FEC',
  Dtx: 'DTX',
  Sfu: 'SFU',
  Ice: 'ICE',
  Turn: 'TURN',
  Stun: 'STUN',
  Dns: 'DNS',
  Mtu: 'MTU',
  Mss: 'MSS',
};

// FieldMeta carries a description but no short label, so derive a readable
// label from the persisted key (e.g. "receiverLatencyMs" -> "Receiver Latency (ms)").
export function humanizeFieldKey(key: string): string {
  const words = key
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .trim()
    .split(/\s+/);
  if (words.length === 0) return key;
  return words
    .map(word => {
      const capitalized = word.charAt(0).toUpperCase() + word.slice(1);
      return FIELD_ACRONYMS[capitalized] ?? capitalized;
    })
    .join(' ');
}

// Group fields in the metadata's declared group order. Fields whose group is
// not described still render, under a synthetic group at the end.
export function groupSettingsFields(
  fields: FieldMeta[],
  groups: SettingsGroupMeta[],
): Array<{ group: SettingsGroupMeta; fields: FieldMeta[] }> {
  const ordered: Array<{ group: SettingsGroupMeta; fields: FieldMeta[] }> = [];
  groups.forEach(group => {
    const matching = fields.filter(field => field.group === group.id);
    if (matching.length > 0) ordered.push({ group, fields: matching });
  });
  const known = new Set(groups.map(group => group.id));
  const ungrouped = fields.filter(field => !known.has(field.group));
  if (ungrouped.length > 0) {
    ordered.push({ group: { id: '_ungrouped', label: '' }, fields: ungrouped });
  }
  return ordered;
}
