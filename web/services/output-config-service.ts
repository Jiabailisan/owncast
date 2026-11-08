// Output process model client (master spec 3.4, api-contract section 9).
// GET/PUT /api/admin/config/output. applyMode is restart-stream: the saved
// value takes effect at the next stream start and is never applied to a
// running stream. The endpoint was delivered as HEAD 6b833e3272.

import { adminRequest, OUTPUT_CONFIG } from '../utils/apis';
import { PendingRestart } from '../interfaces/settings-metadata.model';
import { OutputProcessModel } from '../types/config-section';

export interface RequestOptions {
  signal?: AbortSignal;
}

export interface OutputConfigState {
  processModel: OutputProcessModel;
}

export interface OutputConfigResponse {
  config: OutputConfigState;
  defaults: OutputConfigState;
  pendingRestart: PendingRestart;
}

export interface OutputConfigSaveResponse extends OutputConfigResponse {
  success: boolean;
  message?: string;
}

class OutputConfigService {
  // GET /api/admin/config/output
  public static async getOutputConfig(options: RequestOptions = {}): Promise<OutputConfigResponse> {
    return adminRequest<OutputConfigResponse>(OUTPUT_CONFIG, { signal: options.signal });
  }

  // PUT /api/admin/config/output — body is the RAW config object, not wrapped.
  public static async saveOutputConfig(
    processModel: OutputProcessModel,
    options: RequestOptions = {},
  ): Promise<OutputConfigSaveResponse> {
    return adminRequest<OutputConfigSaveResponse>(OUTPUT_CONFIG, {
      method: 'PUT',
      data: { processModel },
      signal: options.signal,
    });
  }
}

export default OutputConfigService;
