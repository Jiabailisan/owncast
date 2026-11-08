// Typed client for the SRT/WebRTC admin configuration endpoints.
// Contract: docs/architecture/api-contract.md sections 1-3.
//
// The functions are thin on purpose: adminRequest already applies admin
// Basic-auth, the CSRF header, timeouts and structured APIError values
// (including field-level validation errors).

import {
  adminRequest,
  CONFIG_PREFLIGHT,
  SETTINGS_METADATA,
  SRT_CONFIG,
  WEBRTC_CONFIG,
  WEBRTC_TEST_DOMAIN,
} from '../utils/apis';
import { SettingsMetadata } from '../interfaces/settings-metadata.model';
import {
  SRTConfig,
  SRTConfigResponse,
  SRTConfigSaveResponse,
} from '../interfaces/srt-config.model';
import {
  WebRTCConfig,
  WebRTCConfigResponse,
  WebRTCConfigSaveResponse,
  WebRTCDomainTestResponse,
  PreflightResponse,
} from '../interfaces/webrtc-config.model';

export interface RequestOptions {
  signal?: AbortSignal;
}

class AdminSettingsService {
  // GET /api/admin/config/settings-metadata
  public static async getSettingsMetadata(options: RequestOptions = {}): Promise<SettingsMetadata> {
    return adminRequest<SettingsMetadata>(SETTINGS_METADATA, { signal: options.signal });
  }

  // GET /api/admin/config/srt
  public static async getSRTConfig(options: RequestOptions = {}): Promise<SRTConfigResponse> {
    return adminRequest<SRTConfigResponse>(SRT_CONFIG, { signal: options.signal });
  }

  // PUT /api/admin/config/srt — partial objects are treated as replacement of
  // the provided keys; omitted secrets mean unchanged, "" clears.
  public static async saveSRTConfig(
    patch: Partial<SRTConfig>,
    options: RequestOptions = {},
  ): Promise<SRTConfigSaveResponse> {
    return adminRequest<SRTConfigSaveResponse>(SRT_CONFIG, {
      method: 'PUT',
      data: patch,
      signal: options.signal,
    });
  }

  // GET /api/admin/config/webrtc
  public static async getWebRTCConfig(options: RequestOptions = {}): Promise<WebRTCConfigResponse> {
    return adminRequest<WebRTCConfigResponse>(WEBRTC_CONFIG, { signal: options.signal });
  }

  // PUT /api/admin/config/webrtc
  public static async saveWebRTCConfig(
    patch: Partial<WebRTCConfig>,
    options: RequestOptions = {},
  ): Promise<WebRTCConfigSaveResponse> {
    return adminRequest<WebRTCConfigSaveResponse>(WEBRTC_CONFIG, {
      method: 'PUT',
      data: patch,
      signal: options.signal,
    });
  }

  // POST /api/admin/config/preflight
  public static async runPreflight(
    target: 'all' | 'srt' | 'webrtc' = 'all',
    options: RequestOptions = {},
  ): Promise<PreflightResponse> {
    return adminRequest<PreflightResponse>(CONFIG_PREFLIGHT, {
      method: 'POST',
      data: { target },
      signal: options.signal,
      timeoutMs: 30000,
    });
  }

  // POST /api/admin/config/webrtc/test-domain
  public static async testWebRTCDomain(
    domain: string,
    options: RequestOptions = {},
  ): Promise<WebRTCDomainTestResponse> {
    return adminRequest<WebRTCDomainTestResponse>(WEBRTC_TEST_DOMAIN, {
      method: 'POST',
      data: { domain },
      signal: options.signal,
      timeoutMs: 30000,
    });
  }
}

export default AdminSettingsService;
