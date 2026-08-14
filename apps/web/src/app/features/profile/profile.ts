import { HttpErrorResponse } from '@angular/common/http';
import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { firstValueFrom } from 'rxjs';

import { OwnProfile, ProfileEdit, ProfileService } from './profile.service';

type Load = { state: 'loading' } | { state: 'ready' } | { state: 'error'; message: string };

@Component({
  selector: 'app-profile',
  imports: [RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './profile.css',
  template: `
    <main>
      <a class="back" routerLink="/">&larr; Back</a>
      <h1>Your profile</h1>

      @switch (load().state) {
        @case ('loading') {
          <p>Loading…</p>
        }
        @case ('error') {
          <p class="error" role="alert">{{ loadErrorMessage() }}</p>
        }
        @case ('ready') {
          @if (profile(); as p) {
            <div class="stats">
              <div class="stat">
                <span class="value">{{ p.matchesPlayed }}</span>
                <span class="label">Played</span>
              </div>
              <div class="stat">
                <span class="value">{{ p.wins }}</span>
                <span class="label">Wins</span>
              </div>
              <div class="stat">
                <span class="value">{{ p.losses }}</span>
                <span class="label">Losses</span>
              </div>
            </div>

            <form (submit)="save($event)">
              @if (saveError(); as message) {
                <p class="error" role="alert">{{ message }}</p>
              }

              <label>
                <span class="label-text">Email</span>
                <span class="readonly">{{ p.email }}</span>
              </label>

              <label>
                <span class="label-text">Handle</span>
                <span class="readonly">&#64;{{ p.handle }}</span>
                <span class="hint">Your permanent, unique sign-in name. Cannot be changed here.</span>
              </label>

              <label>
                <span class="label-text">Display name</span>
                <input
                  type="text"
                  name="displayName"
                  [value]="displayNameInput()"
                  (input)="onDisplayNameInput($any($event.target).value)"
                />
                <span class="hint">Shown to other players. Leave blank to use your handle instead.</span>
              </label>

              <label>
                <span class="label-text">Avatar reference (optional)</span>
                <input
                  type="text"
                  name="avatarRef"
                  [value]="avatarInput()"
                  (input)="onAvatarInput($any($event.target).value)"
                />
                <span class="hint">Stored for later — cosmetics aren't rendered yet.</span>
              </label>

              <div class="actions">
                <button type="submit" [disabled]="saving() || !dirty()">
                  {{ saving() ? 'Saving…' : 'Save changes' }}
                </button>
                @if (savedJustNow()) {
                  <span class="saved">Saved.</span>
                }
              </div>
            </form>
          }
        }
      }
    </main>
  `,
})
export class Profile {
  readonly #profiles = inject(ProfileService);

  protected readonly load = signal<Load>({ state: 'loading' });
  protected readonly profile = signal<OwnProfile | null>(null);

  protected readonly displayNameInput = signal('');
  protected readonly avatarInput = signal('');
  // A field is sent only once the player actually touches it, mirroring the server's tri-state
  // contract: an untouched field must be OMITTED from the request, not sent as an empty string,
  // or "leave unchanged" would be indistinguishable from "clear."
  readonly #displayNameDirty = signal(false);
  readonly #avatarDirty = signal(false);
  protected readonly dirty = () => this.#displayNameDirty() || this.#avatarDirty();

  protected readonly saving = signal(false);
  protected readonly saveError = signal<string | null>(null);
  protected readonly savedJustNow = signal(false);

  protected readonly loadErrorMessage = () => {
    const l = this.load();
    return l.state === 'error' ? l.message : '';
  };

  constructor() {
    this.#load();
  }

  async #load(): Promise<void> {
    this.load.set({ state: 'loading' });
    try {
      const p = await firstValueFrom(this.#profiles.me());
      this.profile.set(p);
      this.displayNameInput.set(p.displayName === p.handle ? '' : p.displayName);
      this.avatarInput.set(p.avatarRef ?? '');
      this.load.set({ state: 'ready' });
    } catch {
      // Deliberately generic: this endpoint requires auth (the guard already checked), so a
      // failure here is infrastructure trouble, not something the player caused (§42).
      this.load.set({ state: 'error', message: 'Could not load your profile. Try refreshing.' });
    }
  }

  protected onDisplayNameInput(value: string): void {
    this.displayNameInput.set(value);
    this.#displayNameDirty.set(true);
  }

  protected onAvatarInput(value: string): void {
    this.avatarInput.set(value);
    this.#avatarDirty.set(true);
  }

  protected async save(event: Event): Promise<void> {
    event.preventDefault();
    if (this.saving() || !this.dirty()) {
      return;
    }

    const edit: ProfileEdit = {
      ...(this.#displayNameDirty() ? { displayName: this.displayNameInput().trim() } : {}),
      ...(this.#avatarDirty() ? { avatarRef: this.avatarInput().trim() } : {}),
    };

    this.saving.set(true);
    this.saveError.set(null);
    this.savedJustNow.set(false);
    try {
      const updated = await firstValueFrom(this.#profiles.update(edit));
      this.profile.set(updated);
      this.displayNameInput.set(updated.displayName === updated.handle ? '' : updated.displayName);
      this.avatarInput.set(updated.avatarRef ?? '');
      this.#displayNameDirty.set(false);
      this.#avatarDirty.set(false);
      this.savedJustNow.set(true);
    } catch (err) {
      this.saveError.set(messageFor(err));
    } finally {
      this.saving.set(false);
    }
  }
}

function messageFor(err: unknown): string {
  if (err instanceof HttpErrorResponse) {
    const body = err.error as { error?: { message?: string } } | null;
    if (body?.error?.message) {
      return body.error.message;
    }
  }
  return 'Could not save your changes. Please try again.';
}
