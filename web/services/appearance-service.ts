// Admin Appearance customization API (master spec 4.6/7.3/7.4).
// The computed report is part of GET /api/admin/serverconfig; the two custom
// path fields have dedicated POST endpoints that validate on the server.

import {
  adminRequest,
  CUSTOM_FRONTEND_DIR,
  CUSTOM_PLAYER_PATH,
  SERVER_CONFIG,
} from '../utils/apis';
import { AppearanceAdminReport } from '../interfaces/appearance.model';

export interface RequestOptions {
  signal?: AbortSignal;
}

class AppearanceService {
  // GET /api/admin/serverconfig -> appearance block.
  public static async getReport(options: RequestOptions = {}): Promise<AppearanceAdminReport> {
    const serverConfig = await adminRequest<{ appearance?: AppearanceAdminReport }>(SERVER_CONFIG, {
      signal: options.signal,
    });
    if (!serverConfig || !serverConfig.appearance) {
      throw new Error('The server did not return appearance information.');
    }
    return serverConfig.appearance;
  }

  // POST /api/admin/config/customplayerpath
  public static async saveCustomPlayerPath(
    path: string,
    options: RequestOptions = {},
  ): Promise<void> {
    await adminRequest(CUSTOM_PLAYER_PATH, {
      method: 'POST',
      data: { value: path },
      signal: options.signal,
    });
  }

  // POST /api/admin/config/customfrontenddir
  public static async saveCustomFrontendDir(
    directory: string,
    options: RequestOptions = {},
  ): Promise<void> {
    await adminRequest(CUSTOM_FRONTEND_DIR, {
      method: 'POST',
      data: { value: directory },
      signal: options.signal,
    });
  }
}

export default AppearanceService;
