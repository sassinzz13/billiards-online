import { Routes } from '@angular/router';

/**
 * Top-level routes.
 *
 * Feature routes are lazy-loaded via `loadComponent` / `loadChildren`. This matters most for the
 * game feature in Phase 8: Three.js must stay out of the initial bundle so that login, lobby,
 * profile, and leaderboard pages never pay for it (§28, MEMORY.md §18).
 */
export const routes: Routes = [
  {
    path: '',
    loadComponent: () => import('./features/status/status').then((m) => m.Status),
    title: 'Billiards',
  },
  { path: '**', redirectTo: '' },
];
