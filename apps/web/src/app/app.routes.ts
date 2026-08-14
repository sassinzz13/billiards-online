import { Routes } from '@angular/router';

import { anonymousGuard, authGuard } from './core/guards/auth.guard';

/**
 * Top-level routes.
 *
 * Every feature is lazy-loaded via `loadComponent`. This matters most for the game feature in
 * Phase 8: Three.js must stay out of the initial bundle so login, lobby, profile, and leaderboard
 * never pay for it (§28, MEMORY.md §18).
 *
 * The guards decide what to render, not what is permitted. Authorization is enforced server-side on
 * every request; a guard only saves the user from being shown a shell whose calls would all 401.
 */
export const routes: Routes = [
  {
    path: '',
    canActivate: [authGuard],
    loadComponent: () => import('./features/lobby/lobby').then((m) => m.Lobby),
    title: 'Billiards',
  },
  {
    path: 'login',
    canActivate: [anonymousGuard],
    loadComponent: () => import('./features/auth/login').then((m) => m.Login),
    title: 'Sign in · Billiards',
  },
  {
    path: 'signup',
    canActivate: [anonymousGuard],
    loadComponent: () => import('./features/auth/signup').then((m) => m.Signup),
    title: 'Create account · Billiards',
  },
  {
    path: 'profile',
    canActivate: [authGuard],
    loadComponent: () => import('./features/profile/profile').then((m) => m.Profile),
    title: 'Your profile · Billiards',
  },
  {
    path: 'rooms/:id',
    canActivate: [authGuard],
    loadComponent: () => import('./features/rooms/room').then((m) => m.Room),
    title: 'Room · Billiards',
  },
  { path: '**', redirectTo: '' },
];
