import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';

import { Lobby } from './lobby';

const room = (id: string, overrides: Partial<Record<string, unknown>> = {}) => ({
  id, mode: '1v1', ranked: false, shotTimerSeconds: 30, wagerAmount: 0,
  spectatorsAllowed: true, memberCount: 1, capacity: 2, createdAt: '2026-08-14T00:00:00Z',
  ...overrides,
});

describe('Lobby', () => {
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Lobby],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  async function ready(rooms: unknown[] = [], nextCursor = ''): Promise<ComponentFixture<Lobby>> {
    const fixture = TestBed.createComponent(Lobby);
    fixture.detectChanges();
    http.expectOne((r) => r.url === '/api/v1/rooms').flush({ rooms, nextCursor });
    await Promise.resolve();
    fixture.detectChanges();
    return fixture;
  }

  it('renders an empty state with no public rooms', async () => {
    const fixture = await ready([]);
    expect(fixture.nativeElement.textContent).toContain('No open public rooms');
  });

  it('lists public rooms with their member count', async () => {
    const fixture = await ready([room('r1', { memberCount: 1, capacity: 2 })]);
    expect(fixture.nativeElement.textContent).toContain('1 / 2');
  });

  it('shows Full and disables the button for a room at capacity', async () => {
    const fixture = await ready([room('r1', { memberCount: 2, capacity: 2 })]);
    const button: HTMLButtonElement = fixture.nativeElement.querySelector('td button');
    expect(button.disabled).toBe(true);
    expect(button.textContent).toContain('Full');
  });

  it('shows a Load more button only when a next cursor is present', async () => {
    const withMore = await ready([room('r1')], 'cursor-abc');
    expect(withMore.nativeElement.querySelector('.load-more')).not.toBeNull();
  });

  it('hides Load more once the last page has no cursor', async () => {
    const lastPage = await ready([room('r1')], '');
    expect(lastPage.nativeElement.querySelector('.load-more')).toBeNull();
  });

  it('creates a room with the selected visibility and mode', async () => {
    const fixture = await ready([]);

    const visibility: HTMLSelectElement = fixture.nativeElement.querySelectorAll('select')[0];
    visibility.value = 'private';
    visibility.dispatchEvent(new Event('change'));
    const mode: HTMLSelectElement = fixture.nativeElement.querySelectorAll('select')[1];
    mode.value = '2v2';
    mode.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    fixture.nativeElement.querySelectorAll('form')[0].dispatchEvent(new Event('submit'));

    const req = http.expectOne('/api/v1/rooms');
    expect(req.request.body).toEqual({ visibility: 'private', mode: '2v2', ranked: false });
    req.flush({
      id: 'new-room', visibility: 'private', mode: '2v2', ranked: false, shotTimerSeconds: 30,
      wagerAmount: 0, spectatorsAllowed: true, state: 'open', hostUserId: 'u1', capacity: 4,
      createdAt: '2026-08-14T00:00:00Z', members: [],
    });
    await Promise.resolve();
  });

  it('surfaces a create error without navigating away', async () => {
    const fixture = await ready([]);
    fixture.nativeElement.querySelectorAll('form')[0].dispatchEvent(new Event('submit'));

    http.expectOne('/api/v1/rooms').flush(
      { error: { code: 'rooms.rate_limited', message: 'Too many rooms created recently.' } },
      { status: 429, statusText: 'Too Many Requests' },
    );
    await Promise.resolve();
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('Too many rooms created recently.');
  });

  it('disables the join-by-code button until a code is entered', async () => {
    const fixture = await ready([]);
    const button: HTMLButtonElement = fixture.nativeElement.querySelectorAll('form')[1].querySelector('button');
    expect(button.disabled).toBe(true);

    const input: HTMLInputElement = fixture.nativeElement.querySelector('input[placeholder="ABCD1234"]');
    input.value = 'ABCD1234';
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    expect(button.disabled).toBe(false);
  });
});
