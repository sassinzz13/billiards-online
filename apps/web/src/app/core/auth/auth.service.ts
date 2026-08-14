import { Injectable, computed, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

import { apiErrorMessage } from '../networking/api-error';
import { ApiClient } from '../networking/api-client';

export interface AccountSummary {
  readonly id: string;
  readonly handle: string;
  readonly createdAt: string;
}

interface SessionResponse {
  readonly user: AccountSummary;
  readonly email: string;
  readonly expiresAt?: string;
}

/** Signed out, or signed in as someone. `unknown` means the session has not been checked yet. */
export type AuthState =
  | { readonly status: 'unknown' }
  | { readonly status: 'anonymous' }
  | { readonly status: 'authenticated'; readonly account: AccountSummary; readonly email: string };

/**
 * Owns authentication state for the whole application.
 *
 * The session token is never touched here, or anywhere else in the client: it lives in an HttpOnly
 * cookie the browser attaches automatically, which is precisely so that no script — including this
 * one — can read it. What this service holds is only the *account*, and the server remains the
 * authority on whether the session behind it is still valid (ADR 0009).
 *
 * State is a signal, so guards and components read it synchronously without subscribing.
 */
@Injectable({ providedIn: 'root' })
export class AuthService {
  readonly #api = inject(ApiClient);
  readonly #state = signal<AuthState>({ status: 'unknown' });

  readonly state = this.#state.asReadonly();
  readonly account = computed(() => {
    const s = this.#state();
    return s.status === 'authenticated' ? s.account : null;
  });
  readonly isAuthenticated = computed(() => this.#state().status === 'authenticated');
  readonly isResolved = computed(() => this.#state().status !== 'unknown');

  /**
   * Asks the server who the current cookie belongs to.
   *
   * Called once at startup by the auth guard. A 401 is the expected answer for a signed-out
   * visitor, not an error condition.
   */
  async restore(): Promise<void> {
    try {
      const res = await firstValueFrom(this.#api.get<SessionResponse>('/auth/session'));
      this.#state.set({ status: 'authenticated', account: res.user, email: res.email });
    } catch {
      this.#state.set({ status: 'anonymous' });
    }
  }

  async signup(email: string, handle: string, password: string): Promise<void> {
    const res = await this.#post('/auth/signup', { email, handle, password });
    this.#state.set({ status: 'authenticated', account: res.user, email: res.email });
  }

  async login(email: string, password: string): Promise<void> {
    const res = await this.#post('/auth/login', { email, password });
    this.#state.set({ status: 'authenticated', account: res.user, email: res.email });
  }

  /**
   * Ends the session server-side, then clears local state.
   *
   * Deliberately never rejects. The user asked to be signed out, and locally they now are — so
   * propagating a network failure would only strand the caller mid-flow (leaving them on a page
   * that believes they are signed out) for an outcome they cannot act on. The server-side session
   * still expires on its own, and the cookie is cleared by the response when one arrives.
   */
  async logout(): Promise<void> {
    await this.#endSession('/auth/logout');
  }

  /** Revokes every session for this account, on every device. Never rejects, as above. */
  async logoutEverywhere(): Promise<void> {
    await this.#endSession('/auth/logout-all');
  }

  async #endSession(path: string): Promise<void> {
    try {
      await firstValueFrom(this.#api.post<unknown>(path));
    } catch {
      // Swallowed on purpose — see logout(). Local state is cleared either way.
    } finally {
      this.#state.set({ status: 'anonymous' });
    }
  }

  async #post(path: string, body: unknown): Promise<SessionResponse> {
    try {
      return await firstValueFrom(this.#api.post<SessionResponse>(path, body));
    } catch (err) {
      // The server's message is deliberately vague where vagueness matters — "Invalid email or
      // password" is identical for a wrong password and an unknown account, so it cannot be used
      // to discover which addresses are registered. apiErrorMessage only unwraps that message; it
      // never invents a more specific one.
      throw new AuthError(apiErrorMessage(err, 'Something went wrong. Please try again.'));
    }
  }
}

/** A failure with a message safe to show the user. */
export class AuthError extends Error {}
