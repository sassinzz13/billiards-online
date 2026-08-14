import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { firstValueFrom } from 'rxjs';

import { ProfileService } from './profile.service';

describe('ProfileService', () => {
  let svc: ProfileService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    svc = TestBed.inject(ProfileService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('fetches the relative, versioned /users/me path with credentials', async () => {
    const done = firstValueFrom(svc.me());

    const req = http.expectOne('/api/v1/users/me');
    expect(req.request.method).toBe('GET');
    // withCredentials is what carries the session cookie; without it the request is anonymous and
    // the server returns 401 (ADR 0009).
    expect(req.request.withCredentials).toBe(true);
    req.flush({
      id: 'u1', email: 'r@example.com', handle: 'rocket', displayName: 'rocket',
      matchesPlayed: 0, wins: 0, losses: 0, createdAt: '2026-08-14T00:00:00Z',
    });

    expect((await done).handle).toBe('rocket');
  });

  it('sends only the fields the caller included, omitting the rest', async () => {
    const done = firstValueFrom(svc.update({ displayName: 'Rocket' }));

    const req = http.expectOne('/api/v1/users/me');
    expect(req.request.method).toBe('PATCH');
    expect(req.request.body).toEqual({ displayName: 'Rocket' });
    // avatarRef must be genuinely absent, not present-as-undefined — the server's tri-state
    // contract treats "key present" as "field touched" regardless of the value.
    expect('avatarRef' in req.request.body).toBe(false);
    req.flush({
      id: 'u1', email: 'r@example.com', handle: 'rocket', displayName: 'Rocket',
      matchesPlayed: 0, wins: 0, losses: 0, createdAt: '2026-08-14T00:00:00Z',
    });

    await done;
  });

  it('sends an empty string to clear a field, distinct from omitting it', async () => {
    const done = firstValueFrom(svc.update({ displayName: '' }));

    const req = http.expectOne('/api/v1/users/me');
    expect(req.request.body).toEqual({ displayName: '' });
    req.flush({
      id: 'u1', email: 'r@example.com', handle: 'rocket', displayName: 'rocket',
      matchesPlayed: 0, wins: 0, losses: 0, createdAt: '2026-08-14T00:00:00Z',
    });

    await done;
  });

  it('fetches a public profile by id with no credentials required by the caller', async () => {
    const done = firstValueFrom(svc.byId('u2'));

    const req = http.expectOne('/api/v1/users/u2');
    expect(req.request.method).toBe('GET');
    req.flush({
      id: 'u2', handle: 'legend', displayName: 'legend',
      matchesPlayed: 10, wins: 6, losses: 4, createdAt: '2026-08-14T00:00:00Z',
    });

    expect((await done).handle).toBe('legend');
  });
});
