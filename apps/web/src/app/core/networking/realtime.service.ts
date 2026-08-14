import { Injectable, OnDestroy, computed, inject, signal } from '@angular/core';
import { Subject } from 'rxjs';

import { APP_CONFIG, websocketUrl } from '../config/app-config';

/**
 * Wire envelope every message travels in, mirroring game/protocol.Envelope exactly — same field
 * names, same optional-vs-required shape. See docs/protocol.md and ADR 0006.
 */
export interface Envelope {
  readonly v: number;
  readonly type: string;
  readonly seq?: number;
  readonly requestId?: string;
  readonly matchId?: string;
  readonly ts?: number;
  readonly payload?: unknown;
}

export const PROTOCOL_VERSION = 1;

export type ConnectionState = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed';

const BASE_RECONNECT_DELAY_MS = 1000;
const MAX_RECONNECT_DELAY_MS = 30_000;

/**
 * Owns the single realtime connection for the whole application. No component or feature service
 * ever constructs a WebSocket itself (§66) — they inject this, read `state()`, and subscribe to
 * `messages$` filtered to the envelope types they care about.
 *
 * The browser's native WebSocket is used directly rather than a client library: the protocol here
 * is deliberately simple (one envelope shape, JSON only) and the constitution calls for "native
 * browser WebSocket capabilities where appropriate" (§I). The session cookie is attached
 * automatically by the browser on the upgrade request — nothing here ever touches a token, the same
 * reasoning as core/auth's AuthService (ADR 0009).
 *
 * Reconnection is deliberately not smart about *why* the last connection ended: a closed WebSocket
 * in a browser carries no distinction between "the server rejected the handshake" and "the network
 * blipped" (the constructor's error/close events do not expose the HTTP status the upgrade failed
 * with). So every unexpected close is treated the same — retried with exponential backoff, capped —
 * and an unauthenticated visitor simply keeps failing harmlessly at the capped rate rather than
 * being detected and stopped outright. A future feature that wants to react to "give up, show the
 * user an error" can watch `state()` for that instead.
 */
@Injectable({ providedIn: 'root' })
export class RealtimeService implements OnDestroy {
  readonly #config = inject(APP_CONFIG);

  #ws: WebSocket | null = null;
  #reconnectAttempt = 0;
  #reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  // Set immediately before a deliberate close, so the close handler can tell "we did this on
  // purpose" apart from "the connection dropped out from under us" and skip scheduling a reconnect.
  #closingIntentionally = false;

  readonly #state = signal<ConnectionState>('idle');
  readonly state = this.#state.asReadonly();
  readonly isOpen = computed(() => this.#state() === 'open');

  // The highest server-assigned seq observed so far. Phase 13's resync handshake
  // (state.resync { matchId, lastSeq }) will read this; tracked from Phase 5 onward so nothing
  // downstream has to be retrofitted to start counting (docs/protocol.md §6).
  readonly #lastSeq = signal(0);
  readonly lastSeq = this.#lastSeq.asReadonly();

  readonly #messages = new Subject<Envelope>();
  /** Every decoded envelope the server sends, in arrival order. Consumers filter by `type`. */
  readonly messages$ = this.#messages.asObservable();

  /** Opens the connection if not already open or opening. Safe to call repeatedly. */
  connect(): void {
    if (this.#ws && (this.#ws.readyState === WebSocket.OPEN || this.#ws.readyState === WebSocket.CONNECTING)) {
      return;
    }
    this.#clearReconnectTimer();
    this.#open();
  }

  /** Closes the connection and cancels any pending reconnect. Does not schedule a reconnect. */
  disconnect(): void {
    this.#clearReconnectTimer();
    this.#reconnectAttempt = 0;
    this.#closingIntentionally = true;
    this.#ws?.close(1000, 'client disconnect');
    this.#ws = null;
    this.#state.set('closed');
  }

  /**
   * Sends an envelope if the connection is open, filling in `v` so callers never have to. Silently
   * drops the send if the socket is not open — there is no outbound queue on the client side to
   * mirror the server's, since a disconnected browser tab has nothing useful to do with a message
   * that cannot be delivered right now; the caller's own retry (Phase 9's requestId-based
   * idempotency) is what makes that safe once shot submission exists.
   */
  send(envelope: Omit<Envelope, 'v'>): void {
    if (!this.#ws || this.#ws.readyState !== WebSocket.OPEN) {
      return;
    }
    this.#ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...envelope }));
  }

  ngOnDestroy(): void {
    this.disconnect();
  }

  #open(): void {
    this.#state.set(this.#reconnectAttempt > 0 ? 'reconnecting' : 'connecting');
    this.#closingIntentionally = false;

    const ws = new WebSocket(websocketUrl(this.#config));
    this.#ws = ws;

    ws.onopen = () => {
      this.#reconnectAttempt = 0;
      this.#state.set('open');
    };

    ws.onmessage = (event: MessageEvent<string>) => {
      const envelope = this.#decode(event.data);
      if (!envelope) {
        return;
      }
      if (envelope.seq !== undefined && envelope.seq > this.#lastSeq()) {
        this.#lastSeq.set(envelope.seq);
      }
      this.#messages.next(envelope);
    };

    ws.onerror = () => {
      // The browser follows onerror with onclose for a failed connection, so the actual state
      // transition and reconnect scheduling happen there — duplicating it here would double-count
      // the attempt.
    };

    ws.onclose = () => {
      this.#ws = null;
      if (this.#closingIntentionally) {
        return;
      }
      this.#state.set('closed');
      this.#scheduleReconnect();
    };
  }

  #decode(raw: string): Envelope | null {
    try {
      const parsed: unknown = JSON.parse(raw);
      if (
        typeof parsed === 'object' &&
        parsed !== null &&
        'v' in parsed &&
        'type' in parsed &&
        typeof (parsed as { type: unknown }).type === 'string'
      ) {
        return parsed as Envelope;
      }
    } catch {
      // Falls through to the warning below. A malformed message from our own server would be a
      // server bug, but the client must not throw and tear down the message stream over it.
    }
    console.warn('realtime: received a message that does not look like an envelope', raw);
    return null;
  }

  #scheduleReconnect(): void {
    this.#clearReconnectTimer();
    this.#state.set('reconnecting');

    const delay = Math.min(BASE_RECONNECT_DELAY_MS * 2 ** this.#reconnectAttempt, MAX_RECONNECT_DELAY_MS);
    // +/-20% jitter so a server restart does not bring every connected client back at the exact
    // same instant.
    const jittered = delay * (0.8 + Math.random() * 0.4);
    this.#reconnectAttempt++;

    this.#reconnectTimer = setTimeout(() => this.#open(), jittered);
  }

  #clearReconnectTimer(): void {
    if (this.#reconnectTimer !== null) {
      clearTimeout(this.#reconnectTimer);
      this.#reconnectTimer = null;
    }
  }
}
