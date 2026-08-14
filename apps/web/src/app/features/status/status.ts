import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { Router } from '@angular/router';

import { AuthService } from '../../core/auth/auth.service';
import { ApiClient } from '../../core/networking/api-client';

interface HealthResponse {
  readonly status: string;
  readonly version?: number;
}

type Probe = { state: 'checking' } | { state: 'up'; detail: string } | { state: 'down'; detail: string };

/**
 * The signed-in landing page.
 *
 * Still a placeholder — it proves the round trip browser → Traefik → server → PostgreSQL and shows
 * who is signed in. Phase 4 replaces it with the lobby.
 */
@Component({
  selector: 'app-status',
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <main>
      <header>
        <div>
          <h1>Billiards</h1>
          <p class="tagline">Realtime 3D multiplayer billiards</p>
        </div>
        @if (account(); as player) {
          <div class="account">
            <span class="handle">{{ player.handle }}</span>
            <button type="button" class="link" (click)="signOut()" [disabled]="signingOut()">
              {{ signingOut() ? 'Signing out…' : 'Sign out' }}
            </button>
          </div>
        }
      </header>

      <dl>
        <dt>API</dt>
        <dd [class]="probe().state">
          @switch (probe().state) {
            @case ('checking') { <span>checking…</span> }
            @default { <span>{{ detail() }}</span> }
          }
        </dd>
      </dl>

      <button type="button" (click)="check()" [disabled]="probe().state === 'checking'">
        Re-check
      </button>

      <p class="phase">Phase 2 — authentication. No gameplay yet.</p>
    </main>
  `,
  styles: `
    :host { display: block; font-family: system-ui, sans-serif; padding: 3rem 1.5rem; }
    main { max-width: 32rem; margin: 0 auto; }
    header { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem;
             margin-bottom: 2rem; }
    h1 { margin: 0 0 .25rem; font-size: 1.75rem; }
    .tagline { margin: 0; color: #666; }
    .account { text-align: right; font-size: .875rem; }
    .handle { display: block; font-weight: 600; }
    .link { padding: 0; border: 0; background: none; color: #1a73e8; font: inherit;
            font-size: .8125rem; cursor: pointer; text-decoration: underline; }
    .link:disabled { opacity: .5; cursor: default; }
    dl { display: grid; grid-template-columns: auto 1fr; gap: .5rem 1rem; margin: 0 0 1.5rem; }
    dt { font-weight: 600; }
    dd { margin: 0; }
    dd.up { color: #137333; }
    dd.down { color: #b3261e; }
    dd.checking { color: #666; }
    button { padding: .5rem 1rem; border: 1px solid #ccc; border-radius: .25rem;
             background: #fff; cursor: pointer; font: inherit; }
    button:disabled { opacity: .5; cursor: default; }
    .phase { margin-top: 2rem; font-size: .875rem; color: #888; }
  `,
})
export class Status {
  readonly #api = inject(ApiClient);
  readonly #auth = inject(AuthService);
  readonly #router = inject(Router);

  protected readonly account = this.#auth.account;
  protected readonly probe = signal<Probe>({ state: 'checking' });
  protected readonly signingOut = signal(false);

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
      // Intentionally generic: the server does not report why it is unhealthy, and the client must
      // not invent a reason (§42).
      error: () => this.probe.set({ state: 'down', detail: 'unreachable' }),
    });
  }

  protected async signOut(): Promise<void> {
    this.signingOut.set(true);
    try {
      await this.#auth.logout();
      await this.#router.navigateByUrl('/login');
    } finally {
      this.signingOut.set(false);
    }
  }
}
