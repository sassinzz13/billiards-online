import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';

import { ApiClient } from '../../core/networking/api-client';

interface HealthResponse {
  readonly status: string;
  readonly version?: number;
}

type Probe = { state: 'checking' } | { state: 'up'; detail: string } | { state: 'down'; detail: string };

/**
 * Phase 1 placeholder: proves the full request path works end to end.
 *
 *   browser -> Traefik -> server -> PostgreSQL
 *
 * It is deliberately the only route in the application right now. Phase 2 replaces it with the real
 * shell once there is something to sign in to.
 */
@Component({
  selector: 'app-status',
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <main>
      <h1>Billiards</h1>
      <p class="tagline">Realtime 3D multiplayer billiards — development stack</p>

      <dl>
        <dt>API</dt>
        <dd [class]="probe().state">
          @switch (probe().state) {
            @case ('checking') { <span>checking…</span> }
            @case ('up') { <span>{{ detail() }}</span> }
            @case ('down') { <span>{{ detail() }}</span> }
          }
        </dd>
      </dl>

      <button type="button" (click)="check()" [disabled]="probe().state === 'checking'">
        Re-check
      </button>

      <p class="phase">Phase 1 — development infrastructure. No gameplay yet.</p>
    </main>
  `,
  styles: `
    :host { display: block; font-family: system-ui, sans-serif; padding: 3rem 1.5rem; }
    main { max-width: 32rem; margin: 0 auto; }
    h1 { margin: 0 0 .25rem; font-size: 1.75rem; }
    .tagline { margin: 0 0 2rem; color: #666; }
    dl { display: grid; grid-template-columns: auto 1fr; gap: .5rem 1rem; margin: 0 0 1.5rem; }
    dt { font-weight: 600; }
    dd { margin: 0; }
    dd.up { color: #137333; }
    dd.down { color: #b3261e; }
    dd.checking { color: #666; }
    button { padding: .5rem 1rem; border: 1px solid #ccc; border-radius: .25rem;
             background: #fff; cursor: pointer; }
    button:disabled { opacity: .5; cursor: default; }
    .phase { margin-top: 2rem; font-size: .875rem; color: #888; }
  `,
})
export class Status {
  readonly #api = inject(ApiClient);

  protected readonly probe = signal<Probe>({ state: 'checking' });
  protected readonly detail = () => {
    const p = this.probe();
    return p.state === 'checking' ? '' : p.detail;
  };

  constructor() {
    this.check();
  }

  protected check(): void {
    this.probe.set({ state: 'checking' });
    this.#api.get<HealthResponse>('/health').subscribe({
      next: (res) => this.probe.set({ state: 'up', detail: `reachable (v${res.version ?? 1})` }),
      // The message is intentionally generic: the server does not report why it is unhealthy, and
      // the client should not invent a reason. Detail lives in the server logs (§42).
      error: () => this.probe.set({ state: 'down', detail: 'unreachable' }),
    });
  }
}
