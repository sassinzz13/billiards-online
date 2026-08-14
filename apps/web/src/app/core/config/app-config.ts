import { InjectionToken } from '@angular/core';

/**
 * Runtime configuration for the web client.
 *
 * Note what is *not* here: an API base URL host. Traefik serves the app and the API from a single
 * origin — `/` to the web container, `/api` and `/ws` to the server — so every call is relative.
 *
 * That is not an accident of deployment. It is what lets the session cookie work without a `Domain`
 * attribute, keeps `SameSite=Lax` fully effective, and removes CORS from the system entirely.
 * See ADR 0009 and MEMORY.md §17.
 *
 * Because the paths are relative, nothing needs to be baked in at build time and the same container
 * image runs in development and production unchanged.
 */
export interface AppConfig {
  /** Versioned REST prefix. Relative by design — see above. */
  readonly apiBase: string;

  /** Realtime endpoint path. Used from Phase 5; resolved against the current origin at connect time. */
  readonly wsPath: string;
}

export const APP_CONFIG = new InjectionToken<AppConfig>('APP_CONFIG', {
  providedIn: 'root',
  factory: (): AppConfig => ({
    apiBase: '/api/v1',
    wsPath: '/ws',
  }),
});

/**
 * Absolute WebSocket URL for the current origin.
 *
 * Derived rather than configured, so it cannot drift from where the page was actually served —
 * including the ws/wss choice, which must follow the page's protocol.
 */
export function websocketUrl(config: AppConfig, location: Location = window.location): string {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${location.host}${config.wsPath}`;
}
