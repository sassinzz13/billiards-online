import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';

import { AuthService } from '../../core/auth/auth.service';

@Component({
  selector: 'app-login',
  imports: [RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './auth-form.css',
  template: `
    <form (submit)="submit($event)">
      <h1>Sign in</h1>
      <p class="subtitle">Welcome back.</p>

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
        <span class="label-text">Password</span>
        <input
          type="password"
          name="password"
          autocomplete="current-password"
          required
          [value]="password()"
          (input)="password.set($any($event.target).value)"
        />
      </label>

      <button type="submit" [disabled]="busy()">
        {{ busy() ? 'Signing in…' : 'Sign in' }}
      </button>

      <p class="switch">No account? <a routerLink="/signup">Create one</a></p>
    </form>
  `,
})
export class Login {
  readonly #auth = inject(AuthService);
  readonly #router = inject(Router);
  readonly #route = inject(ActivatedRoute);

  protected readonly email = signal('');
  protected readonly password = signal('');
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  protected async submit(event: Event): Promise<void> {
    event.preventDefault();
    if (this.busy()) {
      return;
    }

    this.busy.set(true);
    this.error.set(null);
    try {
      await this.#auth.login(this.email(), this.password());

      // Continue to wherever the guard interrupted them. Only relative paths are honoured — an
      // absolute URL here would turn the redirect parameter into an open redirect.
      const target = this.#route.snapshot.queryParamMap.get('redirect');
      await this.#router.navigateByUrl(target?.startsWith('/') ? target : '/');
    } catch (err) {
      // The server's message is deliberately identical for a wrong password and an unknown
      // account, so this cannot be used to discover which addresses are registered.
      this.error.set(err instanceof Error ? err.message : 'Sign in failed.');
    } finally {
      this.busy.set(false);
    }
  }
}
