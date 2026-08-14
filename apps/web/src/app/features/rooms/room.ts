import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { firstValueFrom } from 'rxjs';

import { apiErrorMessage } from '../../core/networking/api-error';
import { RoomDetail, RoomMember, RoomsService } from './rooms.service';

type Load = { state: 'loading' } | { state: 'ready' } | { state: 'error'; message: string };

/** One seat: either an occupied RoomMember, or an empty placeholder to fill out the grid. */
type Seat = { readonly slot: number } & ({ readonly occupied: true; readonly member: RoomMember } | { readonly occupied: false });

@Component({
  selector: 'app-room',
  imports: [RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './room.css',
  template: `
    <main>
      <a class="back" routerLink="/">&larr; Back to lobby</a>

      @switch (load().state) {
        @case ('loading') {
          <p>Loading…</p>
        }
        @case ('error') {
          <p class="error" role="alert">{{ loadErrorMessage() }}</p>
        }
        @case ('ready') {
          @if (room(); as r) {
            <div class="title-row">
              <h1>{{ r.mode }} room</h1>
              @if (r.ranked) {
                <span class="badge">Ranked</span>
              }
              @if (r.state === 'closed') {
                <span class="badge closed">Closed</span>
              }
            </div>
            <p class="meta">Shot timer {{ r.shotTimerSeconds }}s &middot; {{ r.spectatorsAllowed ? 'Spectators allowed' : 'No spectators' }}</p>

            @if (r.state === 'closed') {
              <p class="notice">This room has closed.</p>
            }

            @if (r.joinCode; as code) {
              <div class="code-panel">
                <span>Share this code to invite players:</span>
                <span class="code">{{ code }}</span>
                <button type="button" (click)="copyCode(code)">{{ justCopied() ? 'Copied' : 'Copy' }}</button>
              </div>
            }

            @if (actionError(); as message) {
              <p class="error" role="alert">{{ message }}</p>
            }

            <div class="sides">
              @for (side of [0, 1]; track side) {
                <div class="side">
                  <h2>Side {{ side === 0 ? 'A' : 'B' }}</h2>
                  @for (seat of seatsFor(side)(); track seat.slot) {
                    @if (seat.occupied) {
                      <div class="seat" [class.you]="seat.member.userId === r.youAre?.userId">
                        <span>
                          {{ seat.member.handle }}
                          @if (seat.member.userId === r.hostUserId) {
                            <span class="host-tag">host</span>
                          }
                        </span>
                        <span class="ready-dot" [class.ready]="seat.member.ready"></span>
                      </div>
                    } @else {
                      <div class="seat empty">Open seat</div>
                    }
                  }
                </div>
              }
            </div>

            @if (r.youAre; as you) {
              <div class="actions">
                <button
                  type="button"
                  [class.primary]="!you.ready"
                  (click)="toggleReady(you.ready)"
                  [disabled]="togglingReady() || r.state === 'closed'"
                >
                  {{ togglingReady() ? 'Updating…' : you.ready ? 'Not ready' : 'Mark ready' }}
                </button>
                <button type="button" class="danger" (click)="leave()" [disabled]="leaving()">
                  {{ leaving() ? 'Leaving…' : 'Leave room' }}
                </button>
                <button type="button" (click)="refresh()" [disabled]="refreshing()">
                  {{ refreshing() ? 'Refreshing…' : 'Refresh' }}
                </button>
              </div>
            }
          }
        }
      }
    </main>
  `,
})
export class Room {
  readonly #rooms = inject(RoomsService);
  readonly #route = inject(ActivatedRoute);
  readonly #router = inject(Router);

  protected readonly load = signal<Load>({ state: 'loading' });
  protected readonly room = signal<RoomDetail | null>(null);
  protected readonly actionError = signal<string | null>(null);
  protected readonly togglingReady = signal(false);
  protected readonly leaving = signal(false);
  protected readonly refreshing = signal(false);
  protected readonly justCopied = signal(false);

  protected readonly loadErrorMessage = () => {
    const l = this.load();
    return l.state === 'error' ? l.message : '';
  };

  constructor() {
    this.#load();
  }

  /** Members seated on one side, padded with empty placeholders out to that side's slot count. */
  protected seatsFor(side: number) {
    return computed<Seat[]>(() => {
      const r = this.room();
      if (!r) {
        return [];
      }
      const slotsPerSide = r.capacity / 2;
      const bySlot = new Map(r.members.filter((m) => m.side === side).map((m) => [m.slot, m]));
      return Array.from({ length: slotsPerSide }, (_, slot) => {
        const member = bySlot.get(slot);
        return member ? { slot, occupied: true as const, member } : { slot, occupied: false as const };
      });
    });
  }

  async #load(): Promise<void> {
    const id = this.#route.snapshot.paramMap.get('id');
    if (!id) {
      this.load.set({ state: 'error', message: 'No room specified.' });
      return;
    }

    this.load.set({ state: 'loading' });
    try {
      const detail = await firstValueFrom(this.#rooms.get(id));
      this.room.set(detail);
      this.load.set({ state: 'ready' });
    } catch (err) {
      // A 404 covers both "no such room" and "private room you are not in" — internal/rooms
      // deliberately does not distinguish them for a non-member (see rooms/service.go's Detail),
      // so this message stays equally generic.
      this.load.set({ state: 'error', message: apiErrorMessage(err, 'Could not load this room.') });
    }
  }

  protected async refresh(): Promise<void> {
    const r = this.room();
    if (!r || this.refreshing()) {
      return;
    }
    this.refreshing.set(true);
    this.actionError.set(null);
    try {
      this.room.set(await firstValueFrom(this.#rooms.get(r.id)));
    } catch (err) {
      this.actionError.set(apiErrorMessage(err, 'Could not refresh this room.'));
    } finally {
      this.refreshing.set(false);
    }
  }

  protected async toggleReady(currentlyReady: boolean): Promise<void> {
    const r = this.room();
    if (!r || this.togglingReady()) {
      return;
    }
    this.togglingReady.set(true);
    this.actionError.set(null);
    try {
      this.room.set(await firstValueFrom(this.#rooms.setReady(r.id, !currentlyReady)));
    } catch (err) {
      this.actionError.set(apiErrorMessage(err, 'Could not update ready status.'));
    } finally {
      this.togglingReady.set(false);
    }
  }

  protected async leave(): Promise<void> {
    const r = this.room();
    if (!r || this.leaving()) {
      return;
    }
    this.leaving.set(true);
    this.actionError.set(null);
    try {
      await firstValueFrom(this.#rooms.leave(r.id));
      await this.#router.navigateByUrl('/');
    } catch (err) {
      this.actionError.set(apiErrorMessage(err, 'Could not leave this room.'));
    } finally {
      this.leaving.set(false);
    }
  }

  protected async copyCode(code: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(code);
      this.justCopied.set(true);
      setTimeout(() => this.justCopied.set(false), 2000);
    } catch {
      // Clipboard access can be denied by the browser; the code is already visible on screen for
      // manual copying, so this failure needs no user-facing error of its own.
    }
  }
}
