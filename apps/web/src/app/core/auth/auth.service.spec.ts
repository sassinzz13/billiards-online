import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthService } from './auth.service';

const account = { id: 'u1', handle: 'rocket', createdAt: '2026-08-14T00:00:00Z' };
const sessionBody = { user: account, email: 'rocket@example.com' };

describe('AuthService', () => {
  let auth: AuthService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    auth = TestBed.inject(AuthService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('starts in the unknown state so guards know to ask the server', () => {
    expect(auth.state().status).toBe('unknown');
    expect(auth.isResolved()).toBe(false);
    expect(auth.isAuthenticated()).toBe(false);
  });

  it('restores an existing session', async () => {
    const done = auth.restore();
    http.expectOne('/api/v1/auth/session').flush(sessionBody);
    await done;

    expect(auth.isAuthenticated()).toBe(true);
    expect(auth.account()?.handle).toBe('rocket');
  });

  // A signed-out visitor gets a 401. That is the expected answer, not a failure to surface.
  it('treats a 401 from the session probe as simply anonymous', async () => {
    const done = auth.restore();
    http.expectOne('/api/v1/auth/session').flush(null, { status: 401, statusText: 'Unauthorized' });
    await done;

    expect(auth.state().status).toBe('anonymous');
    expect(auth.isResolved()).toBe(true);
  });

  it('signs in and holds the account', async () => {
    const done = auth.login('rocket@example.com', 'correct-horse-battery-staple');

    const req = http.expectOne('/api/v1/auth/login');
    expect(req.request.method).toBe('POST');
    // withCredentials is what carries the session cookie; without it the cookie is never sent.
    expect(req.request.withCredentials).toBe(true);
    req.flush(sessionBody);

    await done;
    expect(auth.isAuthenticated()).toBe(true);
  });

  // The server returns the same message for a wrong password and an unknown account, and the client
  // must pass it through rather than inventing a more specific one.
  it('surfaces the server message on a failed login', async () => {
    const done = auth.login('rocket@example.com', 'wrong');
    http.expectOne('/api/v1/auth/login').flush(
      { error: { code: 'auth.invalid_credentials', message: 'Invalid email or password.' } },
      { status: 401, statusText: 'Unauthorized' },
    );

    await expect(done).rejects.toThrow('Invalid email or password.');
    expect(auth.isAuthenticated()).toBe(false);
  });

  it('reports an unreachable server distinctly from a rejection', async () => {
    const done = auth.login('rocket@example.com', 'whatever');
    http.expectOne('/api/v1/auth/login').error(new ProgressEvent('error'), { status: 0 });

    await expect(done).rejects.toThrow(/Cannot reach the server/);
  });

  it('clears state on sign out', async () => {
    const restored = auth.restore();
    http.expectOne('/api/v1/auth/session').flush(sessionBody);
    await restored;

    const done = auth.logout();
    http.expectOne('/api/v1/auth/logout').flush(null, { status: 204, statusText: 'No Content' });
    await done;

    expect(auth.state().status).toBe('anonymous');
    expect(auth.account()).toBeNull();
  });

  // The user asked to be signed out. Leaving the UI showing them as signed in because a network
  // call failed would be worse than a session row outliving its cookie.
  it('clears local state even when the logout request fails', async () => {
    const restored = auth.restore();
    http.expectOne('/api/v1/auth/session').flush(sessionBody);
    await restored;

    const done = auth.logout();
    http.expectOne('/api/v1/auth/logout').error(new ProgressEvent('error'), { status: 500 });
    await done;

    expect(auth.state().status).toBe('anonymous');
  });

  it('signs up and holds the new account', async () => {
    const done = auth.signup('rocket@example.com', 'rocket', 'correct-horse-battery-staple');
    http.expectOne('/api/v1/auth/signup').flush(sessionBody, { status: 201, statusText: 'Created' });
    await done;

    expect(auth.isAuthenticated()).toBe(true);
  });

  // Nothing in the client ever holds the token: it lives in an HttpOnly cookie precisely so that no
  // script, including this service, can read it (ADR 0009).
  it('never stores a token', async () => {
    const done = auth.login('rocket@example.com', 'correct-horse-battery-staple');
    http.expectOne('/api/v1/auth/login').flush({ ...sessionBody, token: 'should-be-ignored' });
    await done;

    expect(JSON.stringify(auth.state())).not.toContain('should-be-ignored');
  });
});
