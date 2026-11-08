// Wire contract for every SDK method, checked against the server's route list.
// sdk/routes.json is generated from the Go router (pkg/api
// routes_manifest_test.go), so a route added to the server without an SDK
// method fails "every route is covered".

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { after, before, beforeEach, describe, it } from 'node:test';

import { OrionBeltClient } from '../src/index.js';
import { LOGIN_REPLY, S } from './fixtures.js';
import { RecordingServer } from './server.js';

// build-test/test -> sdk/routes.json
const MANIFEST = new URL('../../../routes.json', import.meta.url);

type Endpoint = {
  name: string;
  call: (c: OrionBeltClient) => unknown;
  method: string;
  /** As in routes.json. */
  route: string;
  /** Sent without credentials. */
  public?: boolean;
  /** Substring the request body must contain. */
  bodyHas?: string;
  response?: string;
  /** The call returns a URL instead of sending a request. */
  websocket?: boolean;
};

const ENDPOINTS: Endpoint[] = [
  { name: "getGatewayInfo", call: (c) => c.getGatewayInfo(), method: "GET", route: "/api/v1/gateway-info", public: true },
  { name: "getVersion", call: (c) => c.getVersion(), method: "GET", route: "/api/v1/version", public: true },
  { name: "getOpenApiSpec", call: (c) => c.getOpenApiSpec(), method: "GET", route: "/api/v1/openapi.yaml", public: true, response: "openapi: 3.0.3" },
  { name: "getOpenApiIndex", call: (c) => c.getOpenApiIndex(), method: "GET", route: "/api/v1/openapi.json", public: true },
  { name: "registerAgent", call: (c) => c.registerAgent({ name: "a", hostname: "h", port: 22, public_key: "k" }), method: "POST", route: "/api/v1/public/register/agent", bodyHas: "\"hostname\":\"h\"" },
  { name: "registerClient", call: (c) => c.registerClient({ username: "u", email: "e", public_key: "k" }), method: "POST", route: "/api/v1/public/register/client", bodyHas: "\"username\":\"u\"" },
  { name: "issueChallenge", call: (c) => c.issueChallenge("u"), method: "POST", route: "/api/v1/public/auth/challenge", public: true, bodyHas: "\"username\":\"u\"", response: LOGIN_REPLY },
  { name: "loginWithSshSession", call: (c) => c.loginWithSshSession("u", S), method: "POST", route: "/api/v1/public/login", public: true, bodyHas: "\"signature_format\":\"ssh-ed25519\"", response: LOGIN_REPLY },
  { name: "loginWithSshKey", call: (c) => c.loginWithSshKey("u", S, "123456"), method: "POST", route: "/api/v1/public/login/key", public: true, bodyHas: "\"totp_code\":\"123456\"", response: LOGIN_REPLY },
  { name: "loginWithSshJwt", call: (c) => c.loginWithSshJwt("u", S), method: "POST", route: "/api/v1/public/login/token", public: true, bodyHas: "\"challenge\":\"c1\"", response: LOGIN_REPLY },
  { name: "loginWithPassword", call: (c) => c.loginWithPassword("u", "pw", "1"), method: "POST", route: "/api/v1/public/login/password", public: true, bodyHas: "\"password\":\"pw\"", response: LOGIN_REPLY },
  { name: "redeemBrowserBootstrap", call: (c) => c.redeemBrowserBootstrap("code"), method: "POST", route: "/api/v1/public/auth/browser-bootstrap/redeem", public: true, bodyHas: "\"code\":\"code\"", response: LOGIN_REPLY },
  { name: "webauthnLoginBegin", call: (c) => c.webauthnLoginBegin("u"), method: "POST", route: "/api/v1/public/webauthn/login/begin", public: true, bodyHas: "\"username\":\"u\"" },
  { name: "webauthnLoginFinish", call: (c) => c.webauthnLoginFinish("u", { id: "x" }), method: "POST", route: "/api/v1/public/webauthn/login/finish", public: true, bodyHas: "\"response\":{\"id\":\"x\"}", response: LOGIN_REPLY },
  { name: "pluginWebhook", call: (c) => c.pluginWebhook("slack", "events/x", 'POST', { a: 1 }), method: "ANY", route: "/api/v1/public/plugins/:name/*proxyPath", public: true, bodyHas: "\"a\":1" },
  { name: "logout", call: (c) => c.logout(), method: "POST", route: "/api/v1/logout" },
  { name: "getCurrentUser", call: (c) => c.getCurrentUser(), method: "GET", route: "/api/v1/auth/me" },
  { name: "requestBrowserBootstrap", call: (c) => c.requestBrowserBootstrap(), method: "POST", route: "/api/v1/auth/browser-bootstrap" },
  { name: "setPassword", call: (c) => c.setPassword("pw", "1"), method: "POST", route: "/api/v1/auth/password", bodyHas: "\"password\":\"pw\"" },
  { name: "clearPassword", call: (c) => c.clearPassword("1"), method: "DELETE", route: "/api/v1/auth/password", bodyHas: "\"totp_code\":\"1\"" },
  { name: "mfaEnroll", call: (c) => c.mfaEnroll(), method: "POST", route: "/api/v1/mfa/enroll" },
  { name: "mfaConfirm", call: (c) => c.mfaConfirm("1"), method: "POST", route: "/api/v1/mfa/confirm", bodyHas: "\"code\":\"1\"" },
  { name: "mfaDisable", call: (c) => c.mfaDisable("1"), method: "POST", route: "/api/v1/mfa/disable", bodyHas: "\"code\":\"1\"" },
  { name: "mfaStatus", call: (c) => c.mfaStatus(), method: "GET", route: "/api/v1/mfa/status" },
  { name: "webauthnRegisterBegin", call: (c) => c.webauthnRegisterBegin(), method: "POST", route: "/api/v1/webauthn/register/begin" },
  { name: "webauthnRegisterFinish", call: (c) => c.webauthnRegisterFinish({ id: "x" }), method: "POST", route: "/api/v1/webauthn/register/finish", bodyHas: "\"id\":\"x\"" },
  { name: "webauthnCredentials", call: (c) => c.webauthnCredentials(), method: "GET", route: "/api/v1/webauthn/credentials", response: "[]" },
  { name: "webauthnDeleteCredential", call: (c) => c.webauthnDeleteCredential("w1"), method: "DELETE", route: "/api/v1/webauthn/credentials/:id" },
  { name: "listSshKeys", call: (c) => c.listSshKeys(), method: "GET", route: "/api/v1/ssh-keys", response: "[]" },
  { name: "addSshKey", call: (c) => c.addSshKey("laptop", "ssh-ed25519 AAA"), method: "POST", route: "/api/v1/ssh-keys", bodyHas: "\"name\":\"laptop\"" },
  { name: "deleteSshKey", call: (c) => c.deleteSshKey("k1"), method: "DELETE", route: "/api/v1/ssh-keys/:id" },
  { name: "createApiKey", call: (c) => c.createApiKey({ name: "ci", expires_in: 30 }), method: "POST", route: "/api/v1/api-keys", bodyHas: "\"expires_in\":30" },
  { name: "listApiKeys", call: (c) => c.listApiKeys(), method: "GET", route: "/api/v1/api-keys", response: "{\"api_keys\":[]}" },
  { name: "revokeApiKey", call: (c) => c.revokeApiKey("k1"), method: "POST", route: "/api/v1/api-keys/:id/revoke" },
  { name: "deleteApiKey", call: (c) => c.deleteApiKey("k1"), method: "DELETE", route: "/api/v1/api-keys/:id" },
  { name: "listNotifications", call: (c) => c.listNotifications(5), method: "GET", route: "/api/v1/notifications", response: "[]" },
  { name: "unreadNotificationCount", call: (c) => c.unreadNotificationCount(), method: "GET", route: "/api/v1/notifications/unread-count", response: "{\"unread\":3}" },
  { name: "markNotificationRead", call: (c) => c.markNotificationRead("n1"), method: "POST", route: "/api/v1/notifications/:id/read" },
  { name: "markAllNotificationsRead", call: (c) => c.markAllNotificationsRead(), method: "POST", route: "/api/v1/notifications/read-all" },
  { name: "getNotificationPrefs", call: (c) => c.getNotificationPrefs(), method: "GET", route: "/api/v1/notifications/prefs" },
  { name: "putNotificationPrefs", call: (c) => c.putNotificationPrefs({ email_enabled: true }), method: "PUT", route: "/api/v1/notifications/prefs", bodyHas: "\"email_enabled\":true" },
  { name: "listUsers", call: (c) => c.listUsers(), method: "GET", route: "/api/v1/users", response: "[]" },
  { name: "getUser", call: (c) => c.getUser("u1"), method: "GET", route: "/api/v1/users/:id" },
  { name: "createUser", call: (c) => c.createUser({ username: "bob", email: "b@x" }), method: "POST", route: "/api/v1/admin/users", bodyHas: "\"username\":\"bob\"" },
  { name: "updateUser", call: (c) => c.updateUser("u1", { role: "auditor" }), method: "PUT", route: "/api/v1/admin/users/:id", bodyHas: "\"role\":\"auditor\"" },
  { name: "deleteUser", call: (c) => c.deleteUser("u1"), method: "DELETE", route: "/api/v1/admin/users/:id" },
  { name: "listMachines", call: (c) => c.listMachines(), method: "GET", route: "/api/v1/machines", response: "[]" },
  { name: "getMachine", call: (c) => c.getMachine("m1"), method: "GET", route: "/api/v1/machines/:id" },
  { name: "createMachine", call: (c) => c.createMachine({ name: "web", hostname: "10.0.0.1" }), method: "POST", route: "/api/v1/admin/machines", bodyHas: "\"name\":\"web\"" },
  { name: "updateMachine", call: (c) => c.updateMachine("m1", { is_active: false }), method: "PUT", route: "/api/v1/admin/machines/:id", bodyHas: "\"is_active\":false" },
  { name: "deleteMachine", call: (c) => c.deleteMachine("m1", true), method: "DELETE", route: "/api/v1/admin/machines/:id" },
  { name: "kubeReconcile(dryRun)", call: (c) => c.kubeReconcile("m1", true), method: "GET", route: "/api/v1/admin/machines/:id/kubernetes/reconcile" },
  { name: "kubeReconcile", call: (c) => c.kubeReconcile("m1"), method: "POST", route: "/api/v1/admin/machines/:id/kubernetes/reconcile" },
  { name: "getUserPermissions", call: (c) => c.getUserPermissions("u1"), method: "GET", route: "/api/v1/permissions/user/:id", response: "[]" },
  { name: "getMachinePermissions", call: (c) => c.getMachinePermissions("m1"), method: "GET", route: "/api/v1/permissions/machine/:id", response: "[]" },
  { name: "listAllPermissions", call: (c) => c.listAllPermissions(10), method: "GET", route: "/api/v1/admin/permissions", response: "[]" },
  { name: "grantPermission", call: (c) => c.grantPermission({ user_id: "u1", machine_id: "m1", access_type: "ssh" }), method: "POST", route: "/api/v1/admin/permissions", bodyHas: "\"access_type\":\"ssh\"" },
  { name: "updatePermission", call: (c) => c.updatePermission("p1", { remote_users: ["root"] }), method: "PATCH", route: "/api/v1/admin/permissions/:id", bodyHas: "\"remote_users\":[\"root\"]" },
  { name: "revokePermission", call: (c) => c.revokePermission("p1"), method: "DELETE", route: "/api/v1/admin/permissions/:id" },
  { name: "bulkGrantPermissions", call: (c) => c.bulkGrantPermissions({ user_ids: ["u1"], machine_ids: ["m1"], access_type: "ssh" }), method: "POST", route: "/api/v1/admin/permissions/bulk", bodyHas: "\"user_ids\":[\"u1\"]" },
  { name: "bulkRevokePermissions", call: (c) => c.bulkRevokePermissions(["p1", "p2"]), method: "POST", route: "/api/v1/admin/permissions/bulk-revoke", bodyHas: "\"ids\":[\"p1\",\"p2\"]" },
  { name: "effectivePermissions", call: (c) => c.effectivePermissions({ userId: "u1" }), method: "GET", route: "/api/v1/admin/permissions/effective", response: "[]" },
  { name: "listCapabilities", call: (c) => c.listCapabilities("all"), method: "GET", route: "/api/v1/capabilities" },
  { name: "explainAccess", call: (c) => c.explainAccess("web", "root"), method: "GET", route: "/api/v1/access/explain" },
  { name: "listAccessRequests", call: (c) => c.listAccessRequests("pending"), method: "GET", route: "/api/v1/access-requests", response: "[]" },
  { name: "createAccessRequest", call: (c) => c.createAccessRequest({ machine_id: "m1", reason: "deploy", duration: 3600 }), method: "POST", route: "/api/v1/access-requests", bodyHas: "\"reason\":\"deploy\"" },
  { name: "getAccessRequest", call: (c) => c.getAccessRequest("r1"), method: "GET", route: "/api/v1/access-requests/:id" },
  { name: "listPendingAccessRequests", call: (c) => c.listPendingAccessRequests(), method: "GET", route: "/api/v1/admin/access-requests/pending", response: "[]" },
  { name: "approveAccessRequest", call: (c) => c.approveAccessRequest("r1", "u9"), method: "POST", route: "/api/v1/admin/access-requests/:id/approve", bodyHas: "\"reviewer_id\":\"u9\"" },
  { name: "rejectAccessRequest", call: (c) => c.rejectAccessRequest("r1", "u9"), method: "POST", route: "/api/v1/admin/access-requests/:id/reject", bodyHas: "\"reviewer_id\":\"u9\"" },
  { name: "getRiskPolicy", call: (c) => c.getRiskPolicy(), method: "GET", route: "/api/v1/admin/risk-policy" },
  { name: "putRiskPolicy", call: (c) => c.putRiskPolicy({ auto_approve: { enabled: true } }), method: "PUT", route: "/api/v1/admin/risk-policy", bodyHas: "\"auto_approve\"" },
  { name: "listSessions", call: (c) => c.listSessions("active"), method: "GET", route: "/api/v1/sessions", response: "[]" },
  { name: "listActiveSessions", call: (c) => c.listActiveSessions(), method: "GET", route: "/api/v1/sessions/active", response: "[]" },
  { name: "getSession", call: (c) => c.getSession("s1"), method: "GET", route: "/api/v1/sessions/:id" },
  { name: "getSessionContent", call: (c) => c.getSessionContent("s1"), method: "GET", route: "/api/v1/sessions/:id/content", response: "raw" },
  { name: "listAuditLogs", call: (c) => c.listAuditLogs(50), method: "GET", route: "/api/v1/audit-logs", response: "[]" },
  { name: "exportReport", call: (c) => c.exportReport("soc2-access", "csv", { from: "2026-01-01" }), method: "GET", route: "/api/v1/reports/:name/export", response: "a,b" },
  { name: "getUsageDashboard", call: (c) => c.getUsageDashboard(24, 5), method: "GET", route: "/api/v1/dashboard/usage" },
  { name: "listAnomalies", call: (c) => c.listAnomalies(10, "u1"), method: "GET", route: "/api/v1/anomalies", response: "[]" },
  { name: "getSetupStatus", call: (c) => c.getSetupStatus(), method: "GET", route: "/api/v1/setup/status" },
  { name: "getNotificationPolicy", call: (c) => c.getNotificationPolicy(), method: "GET", route: "/api/v1/admin/notifications/policy" },
  { name: "putNotificationPolicy", call: (c) => c.putNotificationPolicy({ allowed_channels: ["in_app"] }), method: "PUT", route: "/api/v1/admin/notifications/policy", bodyHas: "\"allowed_channels\":[\"in_app\"]" },
  { name: "listNotificationTemplates", call: (c) => c.listNotificationTemplates(), method: "GET", route: "/api/v1/admin/notifications/templates", response: "{\"templates\":[]}" },
  { name: "putNotificationTemplate", call: (c) => c.putNotificationTemplate("access_request.approved", { title: "T" }), method: "PUT", route: "/api/v1/admin/notifications/templates/:event", bodyHas: "\"event_type\":\"access_request.approved\"" },
  { name: "deleteNotificationTemplate", call: (c) => c.deleteNotificationTemplate("access_request.approved"), method: "DELETE", route: "/api/v1/admin/notifications/templates/:event" },
  { name: "listPlugins", call: (c) => c.listPlugins(), method: "GET", route: "/api/v1/admin/plugins", response: "{\"plugins\":[]}" },
  { name: "updatePluginConfig", call: (c) => c.updatePluginConfig("slack", true, { token: "x" }), method: "PUT", route: "/api/v1/admin/plugins/:name/config", bodyHas: "\"enabled\":true" },
  { name: "enablePlugin", call: (c) => c.enablePlugin("slack"), method: "POST", route: "/api/v1/admin/plugins/:name/enable", response: "{\"plugin\":{\"name\":\"slack\"}}" },
  { name: "disablePlugin", call: (c) => c.disablePlugin("slack"), method: "POST", route: "/api/v1/admin/plugins/:name/disable", response: "{\"plugin\":{\"name\":\"slack\"}}" },
  { name: "listConnectedAgents", call: (c) => c.listConnectedAgents(), method: "GET", route: "/api/v1/admin/agents/connected", response: "{\"agents\":[\"m1\"]}" },
  { name: "sendAgentCommand", call: (c) => c.sendAgentCommand("m1", "reload"), method: "POST", route: "/api/v1/admin/agents/:machine_id/command", bodyHas: "\"command\":\"reload\"", response: "{\"output\":\"ok\"}" },
  { name: "disconnectAgent", call: (c) => c.disconnectAgent("m1"), method: "POST", route: "/api/v1/admin/agents/:machine_id/disconnect" },
  { name: "generateAgentInstallScript", call: (c) => c.generateAgentInstallScript({ name: "a", os: "linux", gateway_host: "gw", package_base_url: "https://pkgs" }), method: "POST", route: "/api/v1/admin/agents/install-script", bodyHas: "\"gateway_host\":\"gw\"" },
  { name: "getTrustedCa", call: (c) => c.getTrustedCa(), method: "GET", route: "/api/v1/ssh-cert/ca" },
  { name: "issueUserCert", call: (c) => c.issueUserCert("ssh-ed25519 AAA", 8), method: "POST", route: "/api/v1/ssh-cert", bodyHas: "\"ttl_hours\":8" },
  { name: "exportCa", call: (c) => c.exportCa(), method: "GET", route: "/api/v1/admin/ca/export" },
  { name: "listSshCertificates", call: (c) => c.listSshCertificates("user"), method: "GET", route: "/api/v1/admin/ssh-certificates", response: "{\"certificates\":[]}" },
  { name: "revokeSshCertificate", call: (c) => c.revokeSshCertificate("42", "lost"), method: "POST", route: "/api/v1/admin/ssh-certificates/:serial/revoke", bodyHas: "\"reason\":\"lost\"" },
  { name: "listFiles", call: (c) => c.listFiles("web", "/etc", "root"), method: "GET", route: "/api/v1/files/list", response: "{\"path\":\"/etc\"}" },
  { name: "downloadFile", call: (c) => c.downloadFile("web", "/etc/hosts"), method: "GET", route: "/api/v1/files/download", response: "127.0.0.1" },
  { name: "uploadFile", call: (c) => c.uploadFile("web", "/tmp", "a.txt", new TextEncoder().encode("hello"), "root"), method: "POST", route: "/api/v1/files/upload", bodyHas: "filename=\"a.txt\"" },
  { name: "makeDir", call: (c) => c.makeDir("web", "/tmp/x", "root"), method: "POST", route: "/api/v1/files/mkdir", bodyHas: "\"path\":\"/tmp/x\"" },
  { name: "deleteFile", call: (c) => c.deleteFile("web", "/tmp/x"), method: "DELETE", route: "/api/v1/files" },
  { name: "terminalWsUrl", call: (c) => c.terminalWsUrl("web", "root"), method: "GET", route: "/api/v1/terminal/ws", websocket: true },
  { name: "sessionWatchWsUrl", call: (c) => c.sessionWatchWsUrl("s1"), method: "GET", route: "/api/v1/sessions/:id/watch", websocket: true },
];

function routePattern(route: string): RegExp {
  const parts = route
    .replace(/^\/+|\/+$/g, '')
    .split('/')
    .map((s) => (s.startsWith(':') ? '[^/]+' : s.startsWith('*') ? '.+' : s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  return new RegExp(`^/${parts.join('/')}$`);
}

function manifest(): Set<string> {
  const routes = JSON.parse(readFileSync(MANIFEST, 'utf8')) as { method: string; path: string }[];
  return new Set(routes.map((r) => `${r.method} ${r.path}`));
}

describe('route coverage', () => {
  const covered = new Set(ENDPOINTS.map((e) => `${e.method} ${e.route}`));

  it('every route is covered', () => {
    const missing = [...manifest()].filter((r) => !covered.has(r)).toSorted();
    assert.deepEqual(missing, [], `routes with no SDK method: ${missing.join(', ')}`);
  });

  it('no endpoint targets an unknown route', () => {
    const known = manifest();
    const unknown = [...covered].filter((r) => !known.has(r)).toSorted();
    assert.deepEqual(unknown, [], `SDK methods for routes the server does not have: ${unknown.join(', ')}`);
  });
});

describe('endpoint wire contract', () => {
  const server = new RecordingServer();
  let client: OrionBeltClient;

  before(() => server.start());
  after(() => server.stop());
  beforeEach(() => {
    server.reset();
    client = new OrionBeltClient({ baseUrl: server.url, apiKey: 'test-key' });
  });

  for (const ep of ENDPOINTS) {
    it(ep.name, async () => {
      server.reply(200, ep.response ?? '{}');
      const result = await ep.call(client);

      if (ep.websocket) {
        assert.equal(typeof result, 'string');
        const url = new URL(result as string);
        assert.equal(url.protocol, 'ws:');
        assert.match(url.pathname, routePattern(ep.route));
        assert.equal(server.requests.length, 0);
        return;
      }

      const sent = server.last;
      if (ep.method !== 'ANY') {
        assert.equal(sent.method, ep.method);
      }
      assert.match(sent.target.split('?')[0] ?? '', routePattern(ep.route));
      if (ep.public) {
        assert.equal(sent.headers['x-api-key'], undefined);
      } else {
        assert.equal(sent.headers['x-api-key'], 'test-key');
      }
      if (ep.bodyHas) {
        assert.ok(sent.body.toString().includes(ep.bodyHas), sent.body.toString());
      }
    });
  }
});
