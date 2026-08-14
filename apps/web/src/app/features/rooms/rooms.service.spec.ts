import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { firstValueFrom } from 'rxjs';

import { RoomsService } from './rooms.service';

const detail = {
  id: 'r1', visibility: 'public', mode: '1v1', ranked: false, shotTimerSeconds: 30,
  wagerAmount: 0, spectatorsAllowed: true, state: 'open', hostUserId: 'u1', capacity: 2,
  createdAt: '2026-08-14T00:00:00Z', members: [],
};

describe('RoomsService', () => {
  let svc: RoomsService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    svc = TestBed.inject(RoomsService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('lists rooms at the relative, versioned path with credentials', async () => {
    const done = firstValueFrom(svc.list());
    const req = http.expectOne((r) => r.url === '/api/v1/rooms');
    expect(req.request.method).toBe('GET');
    expect(req.request.withCredentials).toBe(true);
    expect(req.request.params.get('limit')).toBe('20');
    // No cursor was passed — the first page must not send an empty/undefined cursor param at all.
    expect(req.request.params.has('cursor')).toBe(false);
    req.flush({ rooms: [], nextCursor: '' });
    await done;
  });

  it('includes the cursor only when paginating past the first page', async () => {
    firstValueFrom(svc.list('abc123'));
    const req = http.expectOne((r) => r.url === '/api/v1/rooms' && r.params.get('cursor') === 'abc123');
    req.flush({ rooms: [], nextCursor: '' });
  });

  it('creates a room', async () => {
    const done = firstValueFrom(svc.create({ visibility: 'public', mode: '1v1' }));
    const req = http.expectOne('/api/v1/rooms');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ visibility: 'public', mode: '1v1' });
    req.flush(detail);
    expect((await done).id).toBe('r1');
  });

  it('joins a room by id', async () => {
    const done = firstValueFrom(svc.join('r1'));
    const req = http.expectOne('/api/v1/rooms/r1/join');
    expect(req.request.method).toBe('POST');
    req.flush(detail);
    await done;
  });

  it('joins a private room by code', async () => {
    const done = firstValueFrom(svc.joinByCode('ABCD1234'));
    const req = http.expectOne('/api/v1/rooms/join-by-code');
    expect(req.request.body).toEqual({ code: 'ABCD1234' });
    req.flush(detail);
    await done;
  });

  it('leaves a room', async () => {
    const done = firstValueFrom(svc.leave('r1'));
    const req = http.expectOne('/api/v1/rooms/r1/leave');
    expect(req.request.method).toBe('POST');
    req.flush(null, { status: 204, statusText: 'No Content' });
    await done;
  });

  it('sets ready state', async () => {
    const done = firstValueFrom(svc.setReady('r1', true));
    const req = http.expectOne('/api/v1/rooms/r1/ready');
    expect(req.request.body).toEqual({ ready: true });
    req.flush(detail);
    await done;
  });
});
