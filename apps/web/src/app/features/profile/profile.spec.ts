import { ComponentFixture } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { Profile } from './profile';

const baseProfile = {
  id: 'u1',
  email: 'rocket@example.com',
  handle: 'rocket',
  displayName: 'rocket', // equals handle: no custom name set yet
  matchesPlayed: 3,
  wins: 2,
  losses: 1,
  createdAt: '2026-08-14T00:00:00Z',
};

describe('Profile', () => {
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Profile],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();

    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  // #load() awaits firstValueFrom before updating any signal, so its continuation runs as a
  // microtask after flush() returns — one Promise.resolve() tick is enough to let it complete
  // before the next detectChanges() reads the DOM.
  async function ready(
    body: Record<string, unknown> | null = baseProfile,
    status = 200,
  ): Promise<ComponentFixture<Profile>> {
    const fixture = TestBed.createComponent(Profile);
    fixture.detectChanges();
    const req = http.expectOne('/api/v1/users/me');
    if (status === 200) {
      req.flush(body as Record<string, unknown>);
    } else {
      req.flush(body, { status, statusText: 'Server Error' });
    }
    await Promise.resolve();
    fixture.detectChanges();
    return fixture;
  }

  it('loads and renders the account, including stats and email', async () => {
    const fixture = await ready();

    const text: string = fixture.nativeElement.textContent;
    expect(text).toContain('rocket@example.com');
    expect(text).toContain('rocket');
    expect(text).toContain('2'); // wins
  });

  it('shows the save button disabled until a field is touched', async () => {
    const fixture = await ready();

    const button: HTMLButtonElement = fixture.nativeElement.querySelector('button[type="submit"]');
    expect(button.disabled).toBe(true);
  });

  it('sends only the touched field, omitting the untouched one', async () => {
    const fixture = await ready();

    const nameInput: HTMLInputElement = fixture.nativeElement.querySelector('input[name="displayName"]');
    nameInput.value = 'Rocket';
    nameInput.dispatchEvent(new Event('input'));
    fixture.detectChanges();

    const button: HTMLButtonElement = fixture.nativeElement.querySelector('button[type="submit"]');
    expect(button.disabled).toBe(false);
    fixture.nativeElement.querySelector('form').dispatchEvent(new Event('submit'));

    const req = http.expectOne('/api/v1/users/me');
    expect(req.request.method).toBe('PATCH');
    expect(req.request.body).toEqual({ displayName: 'Rocket' });
    expect('avatarRef' in req.request.body).toBe(false);
    req.flush({ ...baseProfile, displayName: 'Rocket' });
    await Promise.resolve();
  });

  // The email field must never become an editable input — it is read-only text, never a form
  // control that could be submitted.
  it('never renders email as an editable field', async () => {
    const fixture = await ready();

    const emailInput = fixture.nativeElement.querySelector('input[name="email"]');
    expect(emailInput).toBeNull();
  });

  it('shows a generic message when loading fails', async () => {
    const fixture = await ready(null, 500);

    const text: string = fixture.nativeElement.textContent;
    expect(text).toContain('Could not load your profile');
  });
});
