import assert from 'node:assert/strict';
import { after, before, beforeEach, describe, it } from 'node:test';

import { ApiError, OrionBeltClient } from '../src/index.js';
import { LOGIN_REPLY, S } from './fixtures.js';
import { RecordingServer } from './server.js';

const server = new RecordingServer();

function queryOf(target: string): Record<string, string> {
  return Object.fromEntries(new URL(target, 'http://x').searchParams);
}

// Never answers; settles only when the request's signal aborts.
const neverAnswers: typeof fetch = (_url, init) =>
  new Promise((_resolve, reject) => init?.signal?.addEventListener('abort', () => reject(init.signal?.reason)));

function json(body: Buffer): Record<string, unknown> {
  return JSON.parse(body.toString()) as Record<string, unknown>;
}

describe('OrionBeltClient', () => {
  let client: OrionBeltClient;

  before(() => server.start());
  after(() => server.stop());
  beforeEach(() => {
    server.reset();
    client = new OrionBeltClient({ baseUrl: server.url, apiKey: 'test-key' });
  });

  describe('construction and URLs', () => {
    it('requires a base URL', () => {
      assert.throws(() => new OrionBeltClient({ baseUrl: '   ' }), /baseUrl is required/);
    });

    it('drops trailing slashes from the base URL', async () => {
      await new OrionBeltClient({ baseUrl: `${server.url}///`, apiKey: 'k' }).getSetupStatus();
      assert.equal(server.last.target, '/api/v1/setup/status');
    });

    it('keeps absolute and /api/ paths', () => {
      const c = new OrionBeltClient({ baseUrl: 'https://gw.example.com' });
      assert.equal(c.url('https://other/x'), 'https://other/x');
      assert.equal(c.url('/api/v2/x'), 'https://gw.example.com/api/v2/x');
      assert.equal(c.url('machines'), 'https://gw.example.com/api/v1/machines');
    });

    it('builds WebSocket URLs that follow the scheme', () => {
      assert.equal(new OrionBeltClient({ baseUrl: 'https://gw.example.com' }).terminalWsUrl('web'), 'wss://gw.example.com/api/v1/terminal/ws?machine=web');
      assert.equal(new OrionBeltClient({ baseUrl: 'http://localhost:8080' }).sessionWatchWsUrl('s 1'), 'ws://localhost:8080/api/v1/sessions/s%201/watch');
    });
  });

  describe('auth', () => {
    it('prefers the API key over the session token and adds the bearer token', async () => {
      await new OrionBeltClient({ baseUrl: server.url, apiKey: ' k ', sessionToken: 's', bearerToken: 'b' }).getCurrentUser();
      const h = server.last.headers;
      assert.equal(h['x-api-key'], 'k');
      assert.equal(h['x-session-token'], undefined);
      assert.equal(h['authorization'], 'Bearer b');
    });

    it('uses the session token without an API key', async () => {
      await new OrionBeltClient({ baseUrl: server.url, sessionToken: 's' }).getCurrentUser();
      assert.equal(server.last.headers['x-session-token'], 's');
    });

    it('does not send blank credentials', async () => {
      const c = new OrionBeltClient({ baseUrl: server.url, apiKey: '  ' });
      c.setBearerToken('');
      await c.getCurrentUser();
      assert.equal(server.last.headers['x-api-key'], undefined);
      assert.equal(server.last.headers['authorization'], undefined);
    });

    it('sends the user agent and custom headers', async () => {
      await new OrionBeltClient({ baseUrl: server.url, userAgent: ' automation/1.0 ', headers: { 'X-Trace': 't1' } }).getGatewayInfo();
      assert.equal(server.last.headers['user-agent'], 'automation/1.0');
      assert.equal(server.last.headers['x-trace'], 't1');
    });

    it('forgets session credentials on logout', async () => {
      const c = new OrionBeltClient({ baseUrl: server.url, apiKey: 'k', sessionToken: 's', bearerToken: 'b' });
      await c.logout();
      assert.equal(server.last.target, '/api/v1/logout');
      assert.deepEqual(c.credentials, { apiKey: 'k' });
    });

    it('keeps credentials when logout fails', async () => {
      server.reply(401, '{"error":"unauthorized"}');
      const c = new OrionBeltClient({ baseUrl: server.url, sessionToken: 's' });
      await assert.rejects(c.logout(), ApiError);
      assert.deepEqual(c.credentials, { sessionToken: 's' });
    });

    it('exposes auth headers and credentials', () => {
      const c = new OrionBeltClient({ baseUrl: 'http://x', sessionToken: 's', bearerToken: 'b' });
      assert.deepEqual(c.authHeaders(), { 'X-Session-Token': 's', Authorization: 'Bearer b' });
      assert.deepEqual(c.credentials, { sessionToken: 's', bearerToken: 'b' });
      c.setSessionToken(undefined);
      c.setApiKey('k');
      assert.deepEqual(c.credentials, { apiKey: 'k', bearerToken: 'b' });
    });
  });

  describe('errors', () => {
    const cases: [string, string][] = [
      ['{"error":"machine not found"}', 'machine not found'],
      ['{"message":"nope"}', 'nope'],
      ['plain failure\n', 'plain failure'],
      ['[1]', '[1]'],
      ['{"code":7}', '{"code":7}'],
    ];
    for (const [body, message] of cases) {
      it(`carries status and message for ${JSON.stringify(body)}`, async () => {
        server.reply(404, body);
        const err = await client.getMachine('m1').catch((e: unknown) => e);
        assert.ok(err instanceof ApiError);
        assert.equal(err.statusCode, 404);
        assert.equal(err.serverMessage, message);
        assert.equal(err.message, `api error (404): ${message}`);
      });
    }

    it('formats the status alone when the body is empty', async () => {
      server.reply(500, '');
      await assert.rejects(client.getSessionContent('s1'), { message: 'api error (500)' });
    });

    it('aborts after the timeout', async () => {
      const c = new OrionBeltClient({ baseUrl: 'http://x', timeoutMs: 10, fetch: neverAnswers });
      await assert.rejects(c.getCurrentUser(), { name: 'TimeoutError' });
    });

    it('sends no signal when the timeout is disabled', async () => {
      let signal: AbortSignal | null | undefined = null;
      const capture: typeof fetch = async (_url, init) => {
        signal = init?.signal;
        return new Response('{}');
      };
      await new OrionBeltClient({ baseUrl: 'http://x', timeoutMs: 0, fetch: capture }).getCurrentUser();
      assert.equal(signal, undefined);
    });
  });

  describe('paths and queries', () => {
    it('keeps IDs inside their path segment', async () => {
      await client.getUser('../admin/users?x=1');
      assert.equal(server.last.target, '/api/v1/users/..%2Fadmin%2Fusers%3Fx%3D1');
    });

    it('keeps webhook subpath slashes and escapes segments', async () => {
      await client.pluginWebhook('my plugin', '/a/b c', 'GET');
      assert.equal(server.last.method, 'GET');
      assert.equal(server.last.target, '/api/v1/public/plugins/my%20plugin/a/b%20c');
    });

    it('omits defaults and encodes values', async () => {
      await client.listAuditLogs();
      assert.equal(server.last.target, '/api/v1/audit-logs');
      await client.explainAccess('web 1', 'root', 'scp', 'alice');
      assert.deepEqual(queryOf(server.last.target), { machine: 'web 1', remote_user: 'root', access_type: 'scp', user: 'alice' });
    });

    it('sends archive only when asked', async () => {
      await client.deleteMachine('m1');
      assert.equal(server.last.target, '/api/v1/admin/machines/m1');
      await client.deleteMachine('m1', true);
      assert.equal(server.last.target, '/api/v1/admin/machines/m1?archive=true');
    });

    it('merges report params with the format', async () => {
      server.reply(200, 'a,b\n1,2\n', 'text/csv');
      const data = await client.exportReport('pci-admin-activity', 'csv', { from: '2026-01-01', to: '2026-02-01' });
      assert.equal(new TextDecoder().decode(data), 'a,b\n1,2\n');
      assert.deepEqual(queryOf(server.last.target), { from: '2026-01-01', to: '2026-02-01', format: 'csv' });
    });

    it('filters effective permissions by machine', async () => {
      server.reply(200, '[]');
      await client.effectivePermissions({ machineId: 'm1' });
      assert.deepEqual(queryOf(server.last.target), { machine_id: 'm1' });
    });
  });

  describe('logins', () => {
    it('signs the challenge and stores the session', async () => {
      server.reply(200, LOGIN_REPLY);
      const c = new OrionBeltClient({ baseUrl: server.url });
      const result = await c.loginWithSshSession('alice', S, ' 123456 ');
      assert.equal(result.session_token, 's1');
      const [challenge, login] = server.requests;
      assert.equal(challenge?.target, '/api/v1/public/auth/challenge');
      const body = json(login?.body ?? Buffer.alloc(0));
      assert.equal(body['public_key'], S.publicKey);
      assert.equal(Buffer.from(body['signature'] as string, 'base64').toString(), 'sig:c1');
      assert.equal(body['totp_code'], '123456');
      assert.deepEqual(c.credentials, { sessionToken: 's1', bearerToken: 'a1' });
    });

    it('omits a blank TOTP code', async () => {
      server.reply(200, LOGIN_REPLY);
      await new OrionBeltClient({ baseUrl: server.url }).loginWithSshKey('alice', S);
      assert.equal(json(server.last.body)['totp_code'], undefined);
    });

    it('accepts an async signer', async () => {
      server.reply(200, LOGIN_REPLY);
      const c = new OrionBeltClient({ baseUrl: server.url });
      await c.loginWithSshJwt('alice', { publicKey: 'k', sign: async () => ({ format: 'rsa-sha2-256', signature: new Uint8Array([1]) }) });
      assert.equal(json(server.last.body)['signature_format'], 'rsa-sha2-256');
      assert.deepEqual(c.credentials, { bearerToken: 'a1' });
    });

    it('stores each login credential', async () => {
      server.reply(200, LOGIN_REPLY);
      const c = new OrionBeltClient({ baseUrl: server.url });
      await c.loginWithSshKey('u', S);
      assert.equal(c.credentials.apiKey, 'k1');
      c.setApiKey();
      await c.loginWithPassword('u', 'pw');
      assert.deepEqual(c.credentials, { sessionToken: 's1', bearerToken: 'a1' });
    });

    const logins: [string, (c: OrionBeltClient) => Promise<unknown>][] = [
      ['password', (c) => c.loginWithPassword('u', 'pw')],
      ['ssh session', (c) => c.loginWithSshSession('u', S)],
      ['ssh key', (c) => c.loginWithSshKey('u', S)],
      ['ssh jwt', (c) => c.loginWithSshJwt('u', S)],
    ];
    for (const [name, login] of logins) {
      it(`fails a ${name} login whose reply has no credential`, async () => {
        server.routes.set('/api/v1/public/auth/challenge', '{"challenge":"c1"}');
        server.reply(200, '');
        await assert.rejects(login(new OrionBeltClient({ baseUrl: server.url })), /no .* returned/);
      });
    }

    it('fails on an empty challenge', async () => {
      server.reply(200, '{"challenge":""}');
      await assert.rejects(client.issueChallenge('u'), /no challenge returned/);
    });

    it('leaves the session alone when a reply has no tokens', async () => {
      server.reply(200, '');
      const c = new OrionBeltClient({ baseUrl: server.url, sessionToken: 'old' });
      await c.redeemBrowserBootstrap('code');
      await c.webauthnLoginFinish('u', {});
      assert.deepEqual(c.credentials, { sessionToken: 'old' });
    });
  });

  describe('responses', () => {
    it('turns null and malformed lists into empty arrays', async () => {
      server.reply(200, 'null');
      assert.deepEqual(await client.listMachines(), []);
      server.reply(200, '{"api_keys":null}');
      assert.deepEqual(await client.listApiKeys(), []);
      server.reply(200, '[1]');
      assert.deepEqual(await client.listPlugins(), []);
    });

    it('defaults file list entries', async () => {
      server.reply(200, '{"path":"/","raw":"total 0"}');
      assert.deepEqual((await client.listFiles('web')).entries, []);
      assert.deepEqual(queryOf(server.last.target), { machine: 'web' });
      server.reply(200, '');
      assert.deepEqual(await client.listFiles('web'), { entries: [] });
    });

    it('reads scalar fields and falls back when they are missing', async () => {
      server.reply(200, '{"unread":7}');
      assert.equal(await client.unreadNotificationCount(), 7);
      server.reply(200, '{"output":"drained"}');
      assert.equal(await client.sendAgentCommand('m1', 'drain'), 'drained');
      server.reply(200, '{"id":"cred-1"}');
      assert.equal(await client.webauthnRegisterFinish({}), 'cred-1');
      server.reply(200, '{"plugin":{"name":"slack","enabled":true}}');
      assert.equal((await client.disablePlugin('slack')).enabled, true);

      server.reply(200, '');
      assert.equal(await client.unreadNotificationCount(), 0);
      assert.equal(await client.sendAgentCommand('m1', 'drain'), '');
      assert.equal(await client.webauthnRegisterFinish({}), '');
      assert.deepEqual(await client.enablePlugin('slack'), {});
      assert.deepEqual(await client.disablePlugin('slack'), {});
    });

    it('returns undefined for an empty body', async () => {
      server.reply(200, '');
      assert.equal(await client.request('POST', '/logout'), undefined);
    });

    it('reads the OpenAPI document as text', async () => {
      server.reply(200, 'openapi: 3.0.3\n', 'application/yaml');
      assert.equal(await client.getOpenApiSpec(), 'openapi: 3.0.3\n');
    });

    it('resolves a machine by name', async () => {
      server.reply(200, '[{"id":"m1","name":"db"},{"id":"m2","name":"web"}]');
      assert.equal((await client.getMachineByName('web')).id, 'm2');
      await assert.rejects(client.getMachineByName('missing'), /not found/);
    });
  });

  describe('requests', () => {
    it('uploads multipart with fields and the file', async () => {
      server.reply(200, '{"message":"ok","path":"/tmp/x","size":5}');
      const result = await client.uploadFile('web', '/tmp', 'a.txt', new TextEncoder().encode('hello'), 'root');
      assert.equal(result.size, 5);
      const sent = server.last;
      assert.match(String(sent.headers['content-type']), /^multipart\/form-data; boundary=/);
      const body = sent.body.toString();
      assert.match(body, /name="machine"\r\n\r\nweb\r\n/);
      assert.match(body, /name="user"\r\n\r\nroot\r\n/);
      assert.match(body, /filename="a.txt"/);
      assert.match(body, /\r\n\r\nhello\r\n/);
    });

    it('uploads a Blob without a remote user and tolerates an empty reply', async () => {
      server.reply(200, '');
      assert.deepEqual(await client.uploadFile('web', '/tmp', 'b.bin', new Blob(['x'])), {});
      assert.doesNotMatch(server.last.body.toString(), /name="user"/);
    });

    it('sends the remote user on mkdir only when set', async () => {
      await client.makeDir('web', '/tmp/x');
      assert.deepEqual(json(server.last.body), { machine: 'web', path: '/tmp/x' });
      await client.makeDir('web', '/tmp/x', 'root');
      assert.deepEqual(json(server.last.body), { machine: 'web', path: '/tmp/x', user: 'root' });
    });

    it('uses the path event for a template', async () => {
      await client.putNotificationTemplate('access_request.approved', { event_type: 'other', title: 'T' });
      assert.equal(json(server.last.body)['event_type'], 'access_request.approved');
    });
  });
});
