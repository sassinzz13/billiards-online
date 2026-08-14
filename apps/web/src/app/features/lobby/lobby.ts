import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { firstValueFrom } from 'rxjs';

import { AuthService } from '../../core/auth/auth.service';
import { apiErrorMessage } from '../../core/networking/api-error';
import { CreateRoomInput, Mode, RoomSummary, RoomsService, Visibility } from '../rooms/rooms.service';

/**
 * The landing page for a signed-in player: browse public rooms, create one, or join a private one
 * by code.
 *
 * There is no separate LobbyService — see rooms.service.ts's module comment for why. This component
 * is the entire "lobby" feature; internal/lobby has no backend package yet for the same reason.
 */
@Component({
  selector: 'app-lobby',
  imports: [RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './lobby.css',
  template: `
    <main>
      <header>
        <div>
          <h1>Billiards</h1>
          <p class="tagline">Find a table.</p>
        </div>
        @if (account(); as player) {
          <div class="account">
            <span class="handle">{{ player.handle }}</span>
            <a class="link" routerLink="/profile">Profile</a>
            <button type="button" class="link" (click)="signOut()" [disabled]="signingOut()">
              {{ signingOut() ? 'Signing out…' : 'Sign out' }}
            </button>
          </div>
        }
      </header>

      <section>
        <h2>Create a room</h2>
        <div class="panel">
          @if (createError(); as message) {
            <p class="error" role="alert">{{ message }}</p>
          }
          <form (submit)="create($event)">
            <div class="row">
              <label>
                <span class="label-text">Visibility</span>
                <select [value]="visibility()" (change)="visibility.set($any($event.target).value)">
                  <option value="public">Public</option>
                  <option value="private">Private (join by code)</option>
                </select>
              </label>
              <label>
                <span class="label-text">Mode</span>
                <select [value]="mode()" (change)="mode.set($any($event.target).value)">
                  <option value="1v1">1v1</option>
                  <option value="2v2">2v2</option>
                </select>
              </label>
              <label class="checkbox">
                <input
                  type="checkbox"
                  [checked]="ranked()"
                  (change)="ranked.set($any($event.target).checked)"
                />
                Ranked
              </label>
              <button type="submit" class="primary" [disabled]="creating()">
                {{ creating() ? 'Creating…' : 'Create room' }}
              </button>
            </div>
          </form>
        </div>
      </section>

      <section>
        <h2>Join a private room</h2>
        <div class="panel">
          @if (joinCodeError(); as message) {
            <p class="error" role="alert">{{ message }}</p>
          }
          <form (submit)="submitJoinByCode($event)">
            <div class="row">
              <label>
                <span class="label-text">Join code</span>
                <input
                  type="text"
                  placeholder="ABCD1234"
                  [value]="joinCode()"
                  (input)="joinCode.set($any($event.target).value)"
                />
              </label>
              <button type="submit" [disabled]="joiningByCode() || !joinCode().trim()">
                {{ joiningByCode() ? 'Joining…' : 'Join' }}
              </button>
            </div>
          </form>
        </div>
      </section>

      <section>
        <h2>Public rooms</h2>
        @if (listError(); as message) {
          <p class="error" role="alert">{{ message }}</p>
        }
        @if (rooms().length === 0 && !loadingList()) {
          <p class="empty">No open public rooms right now — create one above.</p>
        } @else {
          <table>
            <thead>
              <tr>
                <th>Mode</th>
                <th>Players</th>
                <th>Shot timer</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              @for (room of rooms(); track room.id) {
                <tr>
                  <td>
                    {{ room.mode }}
                    @if (room.ranked) {
                      <span class="badge">Ranked</span>
                    }
                  </td>
                  <td>{{ room.memberCount }} / {{ room.capacity }}</td>
                  <td>{{ room.shotTimerSeconds }}s</td>
                  <td>
                    <button
                      type="button"
                      (click)="joinRoom(room)"
                      [disabled]="joiningRoomId() === room.id || room.memberCount >= room.capacity"
                    >
                      {{ room.memberCount >= room.capacity ? 'Full' : (joiningRoomId() === room.id ? 'Joining…' : 'Join') }}
                    </button>
                  </td>
                </tr>
              }
            </tbody>
          </table>
        }

        @if (nextCursor()) {
          <button type="button" class="load-more" (click)="loadMore()" [disabled]="loadingList()">
            {{ loadingList() ? 'Loading…' : 'Load more' }}
          </button>
        }
      </section>

      <p class="phase">Phase 4 — lobby and rooms. No live updates yet; refresh to see changes.</p>
    </main>
  `,
})
export class Lobby {
  readonly #rooms = inject(RoomsService);
  readonly #auth = inject(AuthService);
  readonly #router = inject(Router);

  protected readonly account = this.#auth.account;
  protected readonly signingOut = signal(false);

  protected readonly rooms = signal<RoomSummary[]>([]);
  protected readonly nextCursor = signal('');
  protected readonly loadingList = signal(false);
  protected readonly listError = signal<string | null>(null);

  protected readonly visibility = signal<Visibility>('public');
  protected readonly mode = signal<Mode>('1v1');
  protected readonly ranked = signal(false);
  protected readonly creating = signal(false);
  protected readonly createError = signal<string | null>(null);

  protected readonly joinCode = signal('');
  protected readonly joiningByCode = signal(false);
  protected readonly joinCodeError = signal<string | null>(null);

  protected readonly joiningRoomId = signal<string | null>(null);

  constructor() {
    this.loadFirstPage();
  }

  protected async loadFirstPage(): Promise<void> {
    this.rooms.set([]);
    this.nextCursor.set('');
    await this.loadMore();
  }

  protected async loadMore(): Promise<void> {
    this.loadingList.set(true);
    this.listError.set(null);
    try {
      const res = await firstValueFrom(this.#rooms.list(this.nextCursor() || undefined));
      this.rooms.set([...this.rooms(), ...res.rooms]);
      this.nextCursor.set(res.nextCursor);
    } catch (err) {
      this.listError.set(apiErrorMessage(err, 'Could not load rooms.'));
    } finally {
      this.loadingList.set(false);
    }
  }

  protected async create(event: Event): Promise<void> {
    event.preventDefault();
    if (this.creating()) {
      return;
    }

    const input: CreateRoomInput = {
      visibility: this.visibility(),
      mode: this.mode(),
      ranked: this.ranked(),
    };

    this.creating.set(true);
    this.createError.set(null);
    try {
      const room = await firstValueFrom(this.#rooms.create(input));
      await this.#router.navigateByUrl(`/rooms/${room.id}`);
    } catch (err) {
      this.createError.set(apiErrorMessage(err, 'Could not create the room.'));
    } finally {
      this.creating.set(false);
    }
  }

  protected async joinRoom(room: RoomSummary): Promise<void> {
    if (this.joiningRoomId()) {
      return;
    }
    this.joiningRoomId.set(room.id);
    this.listError.set(null);
    try {
      await firstValueFrom(this.#rooms.join(room.id));
      await this.#router.navigateByUrl(`/rooms/${room.id}`);
    } catch (err) {
      this.listError.set(apiErrorMessage(err, 'Could not join that room.'));
      // The room may have just filled or closed — refresh so the listing reflects reality rather
      // than leaving a stale "Join" button the next click would only fail again.
      await this.loadFirstPage();
    } finally {
      this.joiningRoomId.set(null);
    }
  }

  protected async submitJoinByCode(event: Event): Promise<void> {
    event.preventDefault();
    const code = this.joinCode().trim();
    if (!code || this.joiningByCode()) {
      return;
    }

    this.joiningByCode.set(true);
    this.joinCodeError.set(null);
    try {
      const room = await firstValueFrom(this.#rooms.joinByCode(code));
      await this.#router.navigateByUrl(`/rooms/${room.id}`);
    } catch (err) {
      this.joinCodeError.set(apiErrorMessage(err, 'Could not join with that code.'));
    } finally {
      this.joiningByCode.set(false);
    }
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
