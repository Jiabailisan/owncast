// Appearance customization report for the admin UI (master spec 4.6/7.3/7.4).
// Backend source of truth: webserver/handlers/admin/liveStreamingAppearance.go and
// the AppearanceAdminReport schema in openapi.yaml. Computed by the backend; the
// UI only renders it and never re-derives precedence.

export type ActiveFrontend = 'default' | 'custom-player' | 'custom-frontend';

export interface CustomPlayerLoadError {
  message: string;
  // ISO timestamp of the last reported viewer-side load failure.
  at: string;
}

export interface AppearanceAdminReport {
  activeFrontend: ActiveFrontend;
  customPlayerPath: string;
  customFrontendDir: string;
  customPlayerValid: boolean;
  customFrontendValid: boolean;
  customPlayerError?: string;
  customFrontendError?: string;
  customPlayerLoadError?: CustomPlayerLoadError;
}
