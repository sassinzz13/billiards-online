import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { Status } from './status';

describe('Status', () => {
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Status],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();

    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  // The relative path is what keeps the app same-origin with the API. An absolute URL here would
  // silently reintroduce CORS and break the session cookie. See ADR 0009.
  it('probes the API at a relative, versioned path', () => {
    const fixture = TestBed.createComponent(Status);
    fixture.detectChanges();

    const req = http.expectOne('/api/v1/health');
    expect(req.request.method).toBe('GET');
    req.flush({ status: 'ok', version: 1 });
  });

  it('reports the API as reachable on success', () => {
    const fixture = TestBed.createComponent(Status);
    fixture.detectChanges();

    http.expectOne('/api/v1/health').flush({ status: 'ok', version: 1 });
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('reachable');
  });

  // The failure message must stay generic. The server does not explain why it is unhealthy, and
  // the client must not invent a reason (§42).
  it('reports unreachable without inventing a cause', () => {
    const fixture = TestBed.createComponent(Status);
    fixture.detectChanges();

    http.expectOne('/api/v1/health').flush('boom', { status: 500, statusText: 'Server Error' });
    fixture.detectChanges();

    const text: string = fixture.nativeElement.textContent;
    expect(text).toContain('unreachable');
    expect(text).not.toContain('boom');
    expect(text).not.toContain('500');
  });
});
