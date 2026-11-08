import type { FieldError } from '../interfaces/settings-metadata.model';

// eslint-disable-next-line @typescript-eslint/no-require-imports
const semverGt = require('semver/functions/gt');

/* eslint-disable prefer-destructuring */
const ADMIN_USERNAME = process.env.NEXT_PUBLIC_ADMIN_USERNAME;
const ADMIN_STREAMKEY = process.env.NEXT_PUBLIC_ADMIN_STREAMKEY;
export const NEXT_PUBLIC_API_HOST = process.env.NEXT_PUBLIC_API_HOST;

const API_LOCATION = `${NEXT_PUBLIC_API_HOST}api/admin/`;

export const ADMIN_CSRF_HEADER = 'X-Owncast-CSRF-Protection';

export const FETCH_INTERVAL = 15000;

// Current inbound broadcaster info
export const STATUS = `${API_LOCATION}status`;

// Current server config
export const SERVER_CONFIG = `${API_LOCATION}serverconfig`;

// Base url to update config settings
export const SERVER_CONFIG_UPDATE_URL = `${API_LOCATION}config`;

// Get viewer count over time
export const VIEWERS_OVER_TIME = `${API_LOCATION}viewersOverTime`;

// Get the clients currently playing back video, with their playback health
export const PLAYBACK_CLIENT_DETAILS = `${API_LOCATION}viewers/playback`;

// Get currently connected chat clients
export const CONNECTED_CLIENTS = `${API_LOCATION}chat/clients`;

// Disable/enable a single user
export const USER_ENABLED_TOGGLE = `${API_LOCATION}chat/users/setenabled`;

// Get banned IP addresses
export const BANNED_IPS = `${API_LOCATION}chat/users/ipbans`;

// Remove IP ban
export const BANNED_IP_REMOVE = `${API_LOCATION}chat/users/ipbans/remove`;

// Disable/enable a single user
export const USER_SET_MODERATOR = `${API_LOCATION}chat/users/setmoderator`;

// Get a paginated list of all users
export const USERS = `${API_LOCATION}users`;

// Permanently delete a user
export const DELETE_USER = `${API_LOCATION}users/delete`;

// Get the full list of disabled/banned users
export const DISABLED_USERS = `${API_LOCATION}chat/users/disabled`;

// Get the full list of moderators
export const MODERATORS = `${API_LOCATION}chat/users/moderators`;

// Get hardware stats
export const HARDWARE_STATS = `${API_LOCATION}hardwarestats`;

// Get all logs
export const LOGS_ALL = `${API_LOCATION}logs`;

// Get warnings + errors
export const LOGS_WARN = `${API_LOCATION}logs/warnings`;

// Get chat history
export const CHAT_HISTORY = `${API_LOCATION}chat/messages`;

// Get chat history
export const UPDATE_CHAT_MESSGAE_VIZ = `/api/admin/chat/messagevisibility`;

// Plugin management endpoints. Relative URLs go through Next.js's dev proxy
// (next.config.js rewrites /api/* to the backend), avoiding cross-origin
// fetches; in production the admin UI is same-origin so they resolve directly.
export const PLUGINS_LIST = `/api/admin/plugins`;
export const PLUGIN_UPLOAD = `/api/admin/plugins`;
export const PLUGIN_REGISTRY_LIST = `/api/admin/plugin-registry/list`;
export const PLUGIN_REGISTRY_INSTALL = `/api/admin/plugin-registry/install`;
export const pluginActionUrl = (
  slug: string,
  action: 'enable' | 'disable' | 'reload' | 'uninstall',
) => `/api/admin/plugins/${encodeURIComponent(slug)}/${action}`;
export const pluginInstructionsUrl = (slug: string) =>
  `/api/admin/plugins/${encodeURIComponent(slug)}/instructions`;
// Read (GET) / save (POST) a plugin's admin-set config overrides. The admin
// config form auto-rendered from the plugin's manifest config schema uses this.
export const pluginConfigUrl = (slug: string) =>
  `/api/admin/plugins/${encodeURIComponent(slug)}/config`;
export const authGateSettingsUrl = (slug: string) =>
  `/api/admin/plugins/${encodeURIComponent(slug)}/auth-settings`;

// Upload a new custom emoji
export const UPLOAD_EMOJI = `${API_LOCATION}emoji/upload`;

// Delete a custom emoji
export const DELETE_EMOJI = `${API_LOCATION}emoji/delete`;

// Get all access tokens
export const ACCESS_TOKENS = `${API_LOCATION}accesstokens`;

// Delete a single access token
export const DELETE_ACCESS_TOKEN = `${API_LOCATION}accesstokens/delete`;

// Create a new access token
export const CREATE_ACCESS_TOKEN = `${API_LOCATION}accesstokens/create`;

// Get webhooks
export const WEBHOOKS = `${API_LOCATION}webhooks`;

// Delete a single webhook
export const DELETE_WEBHOOK = `${API_LOCATION}webhooks/delete`;

// Create a single webhook
export const CREATE_WEBHOOK = `${API_LOCATION}webhooks/create`;

// hard coded social icons list
export const SOCIAL_PLATFORMS_LIST = `${NEXT_PUBLIC_API_HOST}api/socialplatforms`;

// send a message to the fediverse
export const FEDERATION_MESSAGE_SEND = `${API_LOCATION}federation/send`;

// Get followers
export const FOLLOWERS = `${API_LOCATION}followers`;

// Get followers pending approval
export const FOLLOWERS_PENDING = `${API_LOCATION}followers/pending`;

// Get followers who were blocked or rejected
export const FOLLOWERS_BLOCKED = `${API_LOCATION}followers/blocked`;

// Approve, reject a follow request
export const SET_FOLLOWER_APPROVAL = `${API_LOCATION}followers/approve`;

// Remove a follower without blocking them (they may follow again)
export const REMOVE_FOLLOWER = `${API_LOCATION}followers/remove`;

// List of inbound federated actions that took place.
export const FEDERATION_ACTIONS = `${API_LOCATION}federation/actions`;

export const API_STREAM_HEALTH_METRICS = `${API_LOCATION}metrics/video`;

// Save an array of stream keys
export const UPDATE_STREAM_KEYS = `${API_LOCATION}config/streamkeys`;

export const API_YP_RESET = `${API_LOCATION}yp/reset`;

// SRT/WebRTC configuration and live stream status
// (docs/architecture/api-contract.md).
export const SETTINGS_METADATA = `${API_LOCATION}config/settings-metadata`;
export const SRT_CONFIG = `${API_LOCATION}config/srt`;
export const WEBRTC_CONFIG = `${API_LOCATION}config/webrtc`;
export const CONFIG_PREFLIGHT = `${API_LOCATION}config/preflight`;
export const WEBRTC_TEST_DOMAIN = `${API_LOCATION}config/webrtc/test-domain`;
export const LIVE_STATUS = `${API_LOCATION}live/status`;
export const LIVE_VIEWERS = `${API_LOCATION}live/viewers`;
export const LIVE_FFMPEG = `${API_LOCATION}live/ffmpeg`;

// Public, additive delivery endpoints (no admin auth).
export const WEBRTC_METRICS = `${NEXT_PUBLIC_API_HOST}api/metrics/webrtc`;
export const CUSTOM_PLAYER_ERROR = `${NEXT_PUBLIC_API_HOST}api/custom-player/error`;

const GITHUB_RELEASE_URL = 'https://api.github.com/repos/owncast/owncast/releases/latest';

interface FetchOptions {
  data?: any;
  method?: string;
  auth?: boolean;
}

export function extractAPIErrorMessage(status: number, body?: any, fallbackText = '') {
  if (body && typeof body === 'object') {
    if (typeof body.error === 'string' && body.error.trim() !== '') {
      return body.error;
    }
    if (typeof body.message === 'string' && body.message.trim() !== '') {
      return body.message;
    }
  }

  if (fallbackText.trim() !== '') {
    return fallbackText;
  }

  return `An error has occurred: ${status}`;
}

export async function fetchData<T = any>(url: string, options?: FetchOptions): Promise<T> {
  const { data, method = 'GET', auth = true } = options || {};

  // eslint-disable-next-line no-undef
  const requestOptions: RequestInit = {
    method,
  };

  const headers: Record<string, string> = {};
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase())) {
    headers[ADMIN_CSRF_HEADER] = '1';
  }

  if (data) {
    requestOptions.body = JSON.stringify(data);
  }

  requestOptions.headers = headers;

  if (auth && ADMIN_USERNAME && ADMIN_STREAMKEY) {
    const encoded = btoa(`${ADMIN_USERNAME}:${ADMIN_STREAMKEY}`);
    headers.Authorization = `Basic ${encoded}`;
    requestOptions.mode = 'cors';
    requestOptions.credentials = 'include';
  }

  const response = await fetch(url, requestOptions);
  const text = await response.text();
  let json: T = {} as T;
  if (text) {
    try {
      json = JSON.parse(text) as T;
    } catch {
      if (response.ok) {
        throw new Error('Invalid JSON response from server');
      }
    }
  }

  if (!response.ok) {
    throw new Error(extractAPIErrorMessage(response.status, json, text));
  }
  return json;
}

export interface APIErrorOptions {
  status?: number;
  code?: string;
  fieldErrors?: FieldError[];
  retryAfterSeconds?: number | null;
}

// Typed API error used by the new SRT/WebRTC surfaces. Unlike fetchData (which
// throws a bare Error with a single message), this carries field-level
// validation errors, a machine-readable code and Retry-After so forms and the
// live-status poller can react precisely.
export class APIError extends Error {
  status: number;

  code?: string;

  fieldErrors: FieldError[];

  retryAfterSeconds: number | null;

  constructor(message: string, options: APIErrorOptions = {}) {
    super(message);
    this.name = 'APIError';
    this.status = options.status ?? 0;
    this.code = options.code;
    this.fieldErrors = options.fieldErrors || [];
    this.retryAfterSeconds = options.retryAfterSeconds ?? null;
  }
}

export interface AdminRequestOptions extends FetchOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

// adminRequest is the structured sibling of fetchData: same admin Basic-auth
// and CSRF behavior, but it parses the validation envelope, honors
// Retry-After, supports cancellation/timeout and throws APIError.
export async function adminRequest<T = any>(
  url: string,
  options?: AdminRequestOptions,
): Promise<T> {
  const { data, method = 'GET', auth = true, signal, timeoutMs = 20000 } = options || {};

  const canAbort = typeof AbortController !== 'undefined';
  const controller = canAbort ? new AbortController() : null;
  let timedOut = false;
  let externallyAborted = false;

  const onExternalAbort = () => {
    externallyAborted = true;
    controller?.abort();
  };
  if (signal) {
    if (signal.aborted) onExternalAbort();
    else signal.addEventListener('abort', onExternalAbort);
  }
  const timer =
    timeoutMs > 0 && controller
      ? setTimeout(() => {
          timedOut = true;
          controller.abort();
        }, timeoutMs)
      : null;

  try {
    const requestOptions: RequestInit = {
      method,
      headers: {},
      ...(controller ? { signal: controller.signal } : {}),
    };
    const headers = requestOptions.headers as Record<string, string>;

    if (!['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase())) {
      headers[ADMIN_CSRF_HEADER] = '1';
    }
    if (data !== undefined && data !== null) {
      requestOptions.body = JSON.stringify(data);
    }
    if (auth && ADMIN_USERNAME && ADMIN_STREAMKEY) {
      const encoded = btoa(`${ADMIN_USERNAME}:${ADMIN_STREAMKEY}`);
      headers.Authorization = `Basic ${encoded}`;
      requestOptions.mode = 'cors';
      requestOptions.credentials = 'include';
    }

    const response = await fetch(url, requestOptions);
    const text = await response.text();
    let json: any = {};
    if (text) {
      try {
        json = JSON.parse(text);
      } catch {
        // Non-JSON error bodies are handled by extractAPIErrorMessage below.
      }
    }

    if (!response.ok) {
      const fieldErrors: FieldError[] = Array.isArray(json?.fieldErrors) ? json.fieldErrors : [];
      const retryHeader = response.headers?.get?.('Retry-After') ?? null;
      const retryAfterSeconds = retryHeader ? Number(retryHeader) || null : null;
      throw new APIError(extractAPIErrorMessage(response.status, json, text), {
        status: response.status,
        code: typeof json?.code === 'string' ? json.code : undefined,
        fieldErrors,
        retryAfterSeconds,
      });
    }

    return json as T;
  } catch (error) {
    if (error instanceof APIError) throw error;
    if (timedOut) throw new APIError('Request timed out', { code: 'timeout' });
    if (externallyAborted) throw new APIError('Request aborted', { code: 'network' });
    throw new APIError((error as Error)?.message || 'Network request failed', {
      code: 'network',
    });
  } finally {
    if (timer !== null) clearTimeout(timer);
    if (signal) signal.removeEventListener('abort', onExternalAbort);
  }
}

// fetchText mirrors fetchData's admin auth handling but returns the raw
// response body as text instead of parsing JSON. Used for endpoints that
// serve plain text/markdown (e.g. a plugin's INSTRUCTIONS.md).
export async function fetchText(url: string, options?: FetchOptions) {
  const { method = 'GET', auth = true } = options || {};

  // eslint-disable-next-line no-undef
  const requestOptions: RequestInit = {
    method,
  };

  const headers: Record<string, string> = {};
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase())) {
    headers[ADMIN_CSRF_HEADER] = '1';
  }
  requestOptions.headers = headers;

  if (auth && ADMIN_USERNAME && ADMIN_STREAMKEY) {
    const encoded = btoa(`${ADMIN_USERNAME}:${ADMIN_STREAMKEY}`);
    headers.Authorization = `Basic ${encoded}`;
    requestOptions.mode = 'cors';
    requestOptions.credentials = 'include';
  }

  const response = await fetch(url, requestOptions);
  if (!response.ok) {
    throw new Error(`An error has occurred: ${response.status}`);
  }
  return response.text();
}

export async function getUnauthedData(url: string, options?: FetchOptions) {
  const opts = {
    method: 'GET',
    auth: false,
    ...options,
  };
  return fetchData(url, opts);
}

export async function fetchExternalData(url: string) {
  try {
    const response = await fetch(url, {
      referrerPolicy: 'no-referrer', // Send no referrer header for privacy reasons.
      referrer: '',
    });
    if (!response.ok) {
      const message = `An error has occured: ${response.status}`;
      throw new Error(message);
    }
    const json = await response.json();
    return json;
  } catch (error) {
    console.log(error);
  }
  return {};
}

export async function getGithubRelease() {
  return fetchExternalData(GITHUB_RELEASE_URL);
}

// isNewerVersion reports whether `candidate` is a strictly-newer semver than
// `current`. The shared primitive behind both the plugin-update check and the
// Owncast server-upgrade check. Missing or non-semver version strings can't be
// ordered, so it returns false rather than throwing or nagging about a change
// it can't reason about.
export function isNewerVersion(candidate?: string, current?: string): boolean {
  if (!candidate || !current) {
    return false;
  }
  try {
    return semverGt(candidate, current);
  } catch {
    return false;
  }
}

// isPluginUpdateAvailable reports whether the registry's latest version is
// strictly newer than what's installed. A newer local/dev build (e.g.
// installed 0.3.0 vs a registry 0.2.1) is NOT an update — only an older
// installed version is. Comparing with string inequality would wrongly flag a
// dev build as "needs update" (and prompt a downgrade).
export function isPluginUpdateAvailable(
  installedVersion?: string,
  latestVersion?: string,
): boolean {
  return isNewerVersion(latestVersion, installedVersion);
}

// Make a request to the Github releases API and return the latest release
// version if it's newer than the running server, otherwise null. Uses the same
// semver comparison as the plugin update check (see isNewerVersion).
export async function upgradeVersionAvailable(currentVersion) {
  const recentRelease = await getGithubRelease();
  let recentReleaseVersion = recentRelease?.tag_name;
  // No/failed release info (getGithubRelease returns {} on error): nothing to
  // offer rather than throwing on an undefined tag.
  if (!recentReleaseVersion) {
    return null;
  }

  if (recentReleaseVersion.substr(0, 1) === 'v') {
    recentReleaseVersion = recentReleaseVersion.substr(1);
  }

  return isNewerVersion(recentReleaseVersion, currentVersion) ? recentReleaseVersion : null;
}
