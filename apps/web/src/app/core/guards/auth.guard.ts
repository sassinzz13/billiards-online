import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';

import { AuthService } from '../auth/auth.service';

/**
 * Blocks a route unless the visitor is signed in, sending them to /login otherwise.
 *
 * This is convenience, not security. It decides what to *render*; every endpoint the page then
 * calls re-checks authentication and authorization server-side. A guard runs in code the user
 * controls, so it can be bypassed by anyone who cares to — which is fine, because bypassing it only
 * reveals an empty shell whose requests all return 401 (§13, §36).
 */
export const authGuard: CanActivateFn = async (_route, state) => {
  const auth = inject(AuthService);
  const router = inject(Router);

  // On a hard page load nothing is known yet, so ask the server once. Subsequent navigations read
  // the cached signal.
  if (!auth.isResolved()) {
    await auth.restore();
  }

  if (auth.isAuthenticated()) {
    return true;
  }

  // Remember where they were headed, so signing in continues the journey instead of dumping them
  // on a landing page.
  return router.createUrlTree(['/login'], { queryParams: { redirect: state.url } });
};

/**
 * The inverse: keeps a signed-in user off /login and /signup, which would otherwise be a confusing
 * dead end offering to sign them in again.
 */
export const anonymousGuard: CanActivateFn = async () => {
  const auth = inject(AuthService);
  const router = inject(Router);

  if (!auth.isResolved()) {
    await auth.restore();
  }
  return auth.isAuthenticated() ? router.createUrlTree(['/']) : true;
};
