// Typed client for the admin Live Stream Status endpoints.
// Contract: docs/architecture/api-contract.md section 4.

import { adminRequest, LIVE_FFMPEG, LIVE_STATUS, LIVE_VIEWERS } from '../utils/apis';
import {
  FfmpegPanel,
  LiveStatus,
  LiveViewersQuery,
  LiveViewersResponse,
} from '../interfaces/live-status.model';

export interface RequestOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

// Build the viewers query string. Exported for tests: the live viewer table
// owns pagination/sort/filter state and must produce stable URLs.
export function buildLiveViewersUrl(query: LiveViewersQuery = {}): string {
  const params: string[] = [];
  const add = (key: string, value: string | number | undefined | null) => {
    if (value === undefined || value === null || value === '') return;
    params.push(encodeURIComponent(key) + '=' + encodeURIComponent(String(value)));
  };
  add('page', query.page);
  add('pageSize', query.pageSize);
  add('sort', query.sort);
  add('order', query.order);
  add('protocol', query.protocol);
  add('q', query.q);
  return params.length > 0 ? LIVE_VIEWERS + '?' + params.join('&') : LIVE_VIEWERS;
}

class LiveStatusService {
  // GET /api/admin/live/status
  public static async getStatus(options: RequestOptions = {}): Promise<LiveStatus> {
    return adminRequest<LiveStatus>(LIVE_STATUS, {
      signal: options.signal,
      timeoutMs: options.timeoutMs ?? 8000,
    });
  }

  // GET /api/admin/live/viewers
  public static async getViewers(
    query: LiveViewersQuery = {},
    options: RequestOptions = {},
  ): Promise<LiveViewersResponse> {
    return adminRequest<LiveViewersResponse>(buildLiveViewersUrl(query), {
      signal: options.signal,
      timeoutMs: options.timeoutMs ?? 8000,
    });
  }

  // GET /api/admin/live/ffmpeg
  public static async getFfmpegPanel(options: RequestOptions = {}): Promise<FfmpegPanel> {
    return adminRequest<FfmpegPanel>(LIVE_FFMPEG, {
      signal: options.signal,
      timeoutMs: options.timeoutMs ?? 8000,
    });
  }
}

export default LiveStatusService;
