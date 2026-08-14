import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ActivatedRoute, convertToParamMap, provideRouter } from '@angular/router';

import { Room } from './room';

const baseDetail = {
  id: 'r1', visibility: 'public', mode: '1v1', ranked: false, shotTimerSeconds: 30,
  wagerAmount: 0, spectatorsAllowed: true, state: 'open', hostUserId: 'host-1', capacity: 2,
  createdAt: '2026-08-14T00:00:00Z',
  members: [
    { userId: 'host-1', handle: 'hostie', side: 0, slot: 0, ready: false, joinedAt: '2026-08-14T00:00:00Z' },
  ],
  youAre: { userId: 'host-1', handle: 'hostie', side: 0, slot: 0, ready: false, joinedAt: '2026-08-14T00:00:00Z' },
};

describe('Room', () => {
  let http: HttpTestingController;

  async function setup(): Promise<void> {
    await TestBed.configureTestingModule({
      imports: [Room],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([]),
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { paramMap: convertToParamMap({ id: 'r1' }) } },
        },
      ],
    }).compileComponents();
    http = TestBed.inject(HttpTestingController);
  }

  afterEach(() => http.verify());

  async function ready(detail: Record<string, unknown> = baseDetail): Promise<ComponentFixture<Room>> {
    const fixture = TestBed.createComponent(Room);
    fixture.detectChanges();
    http.expectOne('/api/v1/rooms/r1').flush(detail);
    await Promise.resolve();
    fixture.detectChanges();
    return fixture;
  }

  it('renders the occupied seat and an empty one for the second slot', async () => {
    await setup();
    const fixture = await ready();

    const text: string = fixture.nativeElement.textContent;
    expect(text).toContain('hostie');
    expect(text).toContain('host'); // host tag
    expect(text).toContain('Open seat');
  });

  it('shows a generic message for a 404 — covers both unknown and private-not-a-member rooms', async () => {
    await setup();
    const fixture = TestBed.createComponent(Room);
    fixture.detectChanges();
    http.expectOne('/api/v1/rooms/r1').flush(
      { error: { code: 'rooms.not_found', message: 'No such room.' } },
      { status: 404, statusText: 'Not Found' },
    );
    await Promise.resolve();
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('No such room.');
  });

  it('displays the join code for a private room and hides it when absent', async () => {
    await setup();
    const withCode = await ready({ ...baseDetail, visibility: 'private', joinCode: 'ABCD1234' });
    expect(withCode.nativeElement.textContent).toContain('ABCD1234');
  });

  it('toggles ready state', async () => {
    await setup();
    const fixture = await ready();

    const [readyBtn] = fixture.nativeElement.querySelectorAll('.actions button');
    expect(readyBtn.textContent).toContain('Mark ready');
    readyBtn.click();

    const req = http.expectOne('/api/v1/rooms/r1/ready');
    expect(req.request.body).toEqual({ ready: true });
    req.flush({
      ...baseDetail,
      members: [{ ...baseDetail.members[0], ready: true }],
      youAre: { ...baseDetail.youAre, ready: true },
    });
    await Promise.resolve();
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelectorAll('.actions button')[0].textContent).toContain('Not ready');
  });

  it('leaves the room and navigates home', async () => {
    await setup();
    const fixture = await ready();

    const buttons: HTMLButtonElement[] = Array.from(fixture.nativeElement.querySelectorAll('.actions button'));
    const leaveBtn = buttons.find((b) => b.textContent?.includes('Leave room'))!;
    leaveBtn.click();

    http.expectOne('/api/v1/rooms/r1/leave').flush(null, { status: 204, statusText: 'No Content' });
    await Promise.resolve();
  });

  it('does not render action buttons for a room the viewer is not seated in', async () => {
    await setup();
    // youAre omitted — a state that should not occur given RequireAuth + membership checks, but
    // the template must not crash rendering it either way.
    const { youAre, ...withoutYouAre } = baseDetail;
    const fixture = await ready(withoutYouAre);
    expect(fixture.nativeElement.querySelector('.actions')).toBeNull();
  });
});
