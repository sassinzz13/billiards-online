import { AppConfig, websocketUrl } from './app-config';

describe('websocketUrl', () => {
  const config: AppConfig = { apiBase: '/api/v1', wsPath: '/ws' };

  // The scheme must follow the page's protocol. A ws:// connection from an https:// page is blocked
  // as mixed content, and it would be a silent downgrade if it were not.
  it('uses wss when the page is served over https', () => {
    const url = websocketUrl(config, {
      protocol: 'https:',
      host: 'billiards-online.duckdns.org',
    } as Location);

    expect(url).toBe('wss://billiards-online.duckdns.org/ws');
  });

  it('uses ws for plain http in development', () => {
    const url = websocketUrl(config, {
      protocol: 'http:',
      host: 'billiards.localhost',
    } as Location);

    expect(url).toBe('ws://billiards.localhost/ws');
  });

  // The host is taken from the page, never configured, so the socket cannot be pointed at a
  // different origin than the one that served the app.
  it('preserves a non-default port', () => {
    const url = websocketUrl(config, {
      protocol: 'http:',
      host: 'localhost:4200',
    } as Location);

    expect(url).toBe('ws://localhost:4200/ws');
  });
});
