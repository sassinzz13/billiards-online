import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiClient } from '../../core/networking/api-client';

/** The full record the server returns only to the account's own owner — includes email. */
export interface OwnProfile {
  readonly id: string;
  readonly email: string;
  readonly handle: string;
  readonly displayName: string;
  readonly avatarRef?: string;
  readonly matchesPlayed: number;
  readonly wins: number;
  readonly losses: number;
  readonly createdAt: string;
}

/** What any player sees about any other player. Structurally has no email field. */
export interface PublicProfile {
  readonly id: string;
  readonly handle: string;
  readonly displayName: string;
  readonly avatarRef?: string;
  readonly matchesPlayed: number;
  readonly wins: number;
  readonly losses: number;
  readonly createdAt: string;
}

/**
 * A tri-state edit, mirroring the server's contract exactly: omit a field to leave it unchanged,
 * send an empty string to clear it, send a value to set it (`internal/users/model.go`,
 * `UpdateProfileInput`). There is no field here for *whose* profile — the server always acts on
 * whoever the session belongs to, which is what makes editing someone else's profile structurally
 * impossible rather than merely forbidden.
 */
export interface ProfileEdit {
  readonly displayName?: string;
  readonly avatarRef?: string;
}

/**
 * Talks to `internal/users`' HTTP surface. Kept separate from `core/auth`'s AuthService on purpose:
 * auth answers "who is this session," users answers "what does this player look like" — the same
 * split the backend makes between the `auth` and `users` features.
 */
@Injectable({ providedIn: 'root' })
export class ProfileService {
  readonly #api = inject(ApiClient);

  me(): Observable<OwnProfile> {
    return this.#api.get<OwnProfile>('/users/me');
  }

  update(edit: ProfileEdit): Observable<OwnProfile> {
    return this.#api.patch<OwnProfile>('/users/me', edit);
  }

  byId(id: string): Observable<PublicProfile> {
    return this.#api.get<PublicProfile>(`/users/${id}`);
  }
}
