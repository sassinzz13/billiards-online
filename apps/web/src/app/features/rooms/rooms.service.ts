import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiClient } from '../../core/networking/api-client';

export type Visibility = 'public' | 'private';
export type Mode = '1v1' | '2v2';
export type RoomState = 'open' | 'closed';

/** The public discovery projection — enough to decide whether to join, nothing more. */
export interface RoomSummary {
  readonly id: string;
  readonly mode: Mode;
  readonly ranked: boolean;
  readonly shotTimerSeconds: number;
  readonly wagerAmount: number;
  readonly spectatorsAllowed: boolean;
  readonly memberCount: number;
  readonly capacity: number;
  readonly createdAt: string;
}

export interface RoomMember {
  readonly userId: string;
  readonly handle: string;
  readonly side: number;
  readonly slot: number;
  readonly ready: boolean;
  readonly joinedAt: string;
}

/**
 * The full view of one room. joinCode is present only when the server actually sent it — a public
 * room, or a private room the viewer does not belong to, never carries one (internal/rooms owns
 * that filtering; the client never has to re-derive it).
 */
export interface RoomDetail {
  readonly id: string;
  readonly visibility: Visibility;
  readonly mode: Mode;
  readonly ranked: boolean;
  readonly shotTimerSeconds: number;
  readonly wagerAmount: number;
  readonly spectatorsAllowed: boolean;
  readonly state: RoomState;
  readonly hostUserId: string;
  readonly joinCode?: string;
  readonly capacity: number;
  readonly createdAt: string;
  readonly members: readonly RoomMember[];
  readonly youAre?: RoomMember;
}

export interface CreateRoomInput {
  readonly visibility: Visibility;
  readonly mode: Mode;
  readonly ranked?: boolean;
  readonly shotTimerSeconds?: number;
  readonly wagerAmount?: number;
  readonly spectatorsAllowed?: boolean;
}

interface RoomListResponse {
  readonly rooms: readonly RoomSummary[];
  readonly nextCursor: string;
}

/**
 * Talks to internal/rooms' HTTP surface.
 *
 * There is no separate lobby.service.ts: browsing, creating, and joining by code are all room
 * operations, and the backend has no distinct internal/lobby package yet for the same reason — see
 * PLAN.md Phase 4. features/lobby and features/rooms both depend on this one service.
 */
@Injectable({ providedIn: 'root' })
export class RoomsService {
  readonly #api = inject(ApiClient);

  list(cursor?: string, limit = 20): Observable<RoomListResponse> {
    return this.#api.get<RoomListResponse>('/rooms', { limit, cursor });
  }

  create(input: CreateRoomInput): Observable<RoomDetail> {
    return this.#api.post<RoomDetail>('/rooms', input);
  }

  get(id: string): Observable<RoomDetail> {
    return this.#api.get<RoomDetail>(`/rooms/${id}`);
  }

  join(id: string): Observable<RoomDetail> {
    return this.#api.post<RoomDetail>(`/rooms/${id}/join`);
  }

  joinByCode(code: string): Observable<RoomDetail> {
    return this.#api.post<RoomDetail>('/rooms/join-by-code', { code });
  }

  leave(id: string): Observable<void> {
    return this.#api.post<void>(`/rooms/${id}/leave`);
  }

  setReady(id: string, ready: boolean): Observable<RoomDetail> {
    return this.#api.post<RoomDetail>(`/rooms/${id}/ready`, { ready });
  }
}
