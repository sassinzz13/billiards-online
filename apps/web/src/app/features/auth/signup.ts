import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';

import { AuthService } from '../../core/auth/auth.service';

/**
 * Client-side limits mirroring the server's. They exist to give immediate feedback, not to enforce
 * anything: the server validates every field again and is the only authority. Anyone can edit these
 * numbers in their own browser, which is exactly why nothing depends on them (§13).
 */
const MIN_PASSWORD = 10;
const HANDLE_PATTERN = /^[A-Za-z0-9_]{3,24}$/;

@Component({
  selector: 'app-signup',
  imports: [RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './auth-form.css',
  template: `
    <form (submit)="submit($event)">
      <h1>Create account</h1>
      <p class="subtitle">Pick a handle other players will see.</p>

      @if (error(); as message) {
        <p class="error" role="alert">{{ message }}</p>
      }

      <label>
        <span class="label-text">Email</span>
        <input
          type="email"
          name="email"
          autocomplete="email"
          required
          [value]="email()"
          (input)="email.set($any($event.target).value)"
        />
      </label>

      <label>
        <span class="label-text">Handle</span>
        <input
          type="text"
          name="handle"
          autocomplete="username"
          required
          [value]="handle()"
          (input)="handle.set($any($event.target).value)"
        />
        <span class="hint">3–24 characters: letters, digits, or underscore.</span>
      </label>

      <label>
        <span class="label-text">Password</span>
        <input
          type="password"
          name="password"
          autocomplete="new-password"
          required
          [value]="password()"
          (input)="password.set($any($event.target).value)"
        />
        <span class="hint">At least {{ minPassword }} characters.</span>
      </label>

      <button type="submit" [disabled]="busy() || !valid()">
        {{ busy() ? 'Creating…' : 'Create account' }}
      </button>

      <p class="switch">Already have an account? <a routerLink="/login">Sign in</a></p>
    </form>
  `,
})
export class Signup {
  readonly #auth = inject(AuthService);
  readonly #router = inject(Router);

  protected readonly minPassword = MIN_PASSWORD;

  protected readonly email = signal('');
  protected readonly handle = signal('');
  protected readonly password = signal('');
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  protected readonly valid = computed(
    () =>
      this.email().includes('@') &&
      HANDLE_PATTERN.test(this.handle()) &&
      this.password().length >= MIN_PASSWORD,
  );

  protected async submit(event: Event): Promise<void> {
    event.preventDefault();
    if (this.busy() || !this.valid()) {
      return;
    }

    this.busy.set(true);
    this.error.set(null);
    try {
      // Signup signs the player straight in — the server issues a session with the account, so
      // there is no reason to make them type the same password again.
      await this.#auth.signup(this.email(), this.handle(), this.password());
      await this.#router.navigateByUrl('/');
    } catch (err) {
      this.error.set(err instanceof Error ? err.message : 'Could not create the account.');
    } finally {
      this.busy.set(false);
    }
  }
}
