import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { APP_CONFIG } from '../config/app-config';

/**
 * Thin wrapper over HttpClient that owns the API base path and credential policy.
 *
 * Every REST call goes through here so that two things are decided in exactly one place:
 *
 *  - the `/api/v1` prefix, so introducing `/api/v2` is a single edit;
 *  - `withCredentials`, so the session cookie is sent on every request without each caller
 *    remembering to opt in.
 *
 * This is deliberately not a repository or a generic data layer. Features own their own request
 * shapes and response types; this only removes the two decisions that must never vary.
 *
 * The realtime WebSocket connection is *not* handled here. It belongs to `core/networking`'s
 * RealtimeService in Phase 5, which owns connection lifecycle, reconnect, and sequencing (§66).
 */
@Injectable({ providedIn: 'root' })
export class ApiClient {
  readonly #http = inject(HttpClient);
  readonly #config = inject(APP_CONFIG);

  get<T>(path: string): Observable<T> {
    return this.#http.get<T>(this.#url(path), { withCredentials: true });
  }

  post<T>(path: string, body?: unknown): Observable<T> {
    return this.#http.post<T>(this.#url(path), body ?? {}, { withCredentials: true });
  }

  patch<T>(path: string, body: unknown): Observable<T> {
    return this.#http.patch<T>(this.#url(path), body, { withCredentials: true });
  }

  #url(path: string): string {
    return `${this.#config.apiBase}${path.startsWith('/') ? path : `/${path}`}`;
  }
}
