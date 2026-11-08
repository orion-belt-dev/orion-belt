// Client for the Orion Belt HTTP API. The Go SDK in pkg/sdk is the reference
// for auth and request behaviour; this client sends the same requests over the
// platform fetch, so it runs in Node 20+, Deno, Bun and browsers.

import type {
  AccessAnomaly,
  AccessExplanation,
  AccessRequest,
  AgentInstallScriptRequest,
  AgentInstallScriptResponse,
  APIKey,
  APIKeyLoginResult,
  AuditLog,
  AuthUser,
  BootstrapCode,
  BulkGrantRequest,
  BulkResult,
  CapabilityCatalog,
  CreateAccessRequestRequest,
  CreateAPIKeyRequest,
  CreateAPIKeyResponse,
  CreateMachineRequest,
  CreateUserRequest,
  EffectiveAccess,
  FileListResponse,
  FileUploadResponse,
  GatewayInfo,
  GrantPermissionRequest,
  IssuedCert,
  JWTLoginResult,
  KubeReconcileResult,
  Machine,
  MFAEnrollResponse,
  MFAStatus,
  Notification,
  NotificationPolicy,
  NotificationPolicyResult,
  NotificationPrefs,
  NotificationPrefsResult,
  NotificationTemplate,
  NotificationTemplateEntry,
  PasswordLoginResult,
  Permission,
  PluginConfigResult,
  PluginInfo,
  RegisterAgentRequest,
  RegisterAgentResponse,
  RegisterClientRequest,
  RegisterClientResponse,
  RiskPolicy,
  Session,
  SetupStatus,
  SSHCertificate,
  SSHKey,
  TrustedCA,
  UpdateMachineRequest,
  UpdatePermissionRequest,
  UpdateUserRequest,
  UsageDashboard,
  User,
  VersionInfo,
  WebAuthnCredential,
} from './types.js';

const DEFAULT_TIMEOUT_MS = 30_000;

export type ClientOptions = {
  /** Server base URL, e.g. https://pam.example.com. */
  baseUrl: string;
  apiKey?: string;
  sessionToken?: string;
  bearerToken?: string;
  /** Per-request timeout; 0 disables it. Defaults to 30 s. */
  timeoutMs?: number;
  /**
   * Sent as User-Agent where the runtime allows it (not in browsers). The
   * gateway records it as the device signal on access requests.
   */
  userAgent?: string;
  headers?: Record<string, string>;
  /** fetch implementation; defaults to the global one. */
  fetch?: typeof fetch;
};

/**
 * Proves possession of an SSH key for the challenge logins. `publicKey` is
 * the authorized_keys line; `sign` returns the SSH signature format
 * (ssh-ed25519, rsa-sha2-256, ...) and the raw signature blob over `data`.
 */
export interface Signer {
  readonly publicKey: string;
  sign(data: Uint8Array): Promise<{ format: string; signature: Uint8Array }> | { format: string; signature: Uint8Array };
}

/** A non-2xx response. `message` is the server's `error` field when it sent one. */
export class ApiError extends Error {
  readonly statusCode: number;
  readonly body: string;
  readonly serverMessage: string;

  constructor(statusCode: number, serverMessage: string, body: string) {
    const detail = serverMessage || body;
    super(detail ? `api error (${statusCode}): ${detail}` : `api error (${statusCode})`);
    this.name = 'ApiError';
    this.statusCode = statusCode;
    this.serverMessage = serverMessage;
    this.body = body;
  }
}

type QueryValue = string | number | boolean | undefined | null;

/** Escapes one path segment so an ID can never add or climb path levels. */
function seg(value: string): string {
  return encodeURIComponent(value);
}

/** Encodes the set parameters; empty, zero and false mean "use the server default". */
function query(params: Record<string, QueryValue>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '' || value === false) {
      continue;
    }
    if (typeof value === 'number' && !(value > 0)) {
      continue;
    }
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : '';
}

function trimToUndefined(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

function trimTrailingSlashes(value: string): string {
  // A loop rather than /\/+$/: that regex backtracks quadratically on a long
  // run of slashes that is not at the end.
  let end = value.length;
  while (end > 0 && value[end - 1] === '/') {
    end--;
  }
  return value.slice(0, end);
}

function errorMessage(body: string): string {
  try {
    const parsed: unknown = JSON.parse(body);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      for (const key of ['error', 'message']) {
        const value = (parsed as Record<string, unknown>)[key];
        if (typeof value === 'string' && value) {
          return value;
        }
      }
    }
  } catch {
    // Not JSON: the raw text is the message.
  }
  return body.trim();
}

function base64(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

type LoginReply = { session_token?: string; access_token?: string; api_key?: string };

/**
 * Client for one Orion Belt server. Auth precedence matches the server:
 * X-API-Key wins over X-Session-Token; a bearer token is sent alongside
 * either. The login helpers store the credential they obtain on the client.
 */
export class OrionBeltClient {
  readonly baseUrl: string;
  private readonly timeoutMs: number;
  private readonly headers: Record<string, string>;
  private readonly fetchImpl: typeof fetch;
  private apiKey: string | undefined;
  private sessionToken: string | undefined;
  private bearerToken: string | undefined;

  constructor(options: ClientOptions) {
    const baseUrl = trimTrailingSlashes(options.baseUrl.trim());
    if (!baseUrl) {
      throw new Error('baseUrl is required');
    }
    this.baseUrl = baseUrl;
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.headers = { ...options.headers };
    const userAgent = trimToUndefined(options.userAgent);
    if (userAgent) {
      this.headers['User-Agent'] = userAgent;
    }
    this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.apiKey = trimToUndefined(options.apiKey);
    this.sessionToken = trimToUndefined(options.sessionToken);
    this.bearerToken = trimToUndefined(options.bearerToken);
  }

  setApiKey(apiKey?: string): void {
    this.apiKey = trimToUndefined(apiKey);
  }

  setSessionToken(sessionToken?: string): void {
    this.sessionToken = trimToUndefined(sessionToken);
  }

  setBearerToken(bearerToken?: string): void {
    this.bearerToken = trimToUndefined(bearerToken);
  }

  /** Current credentials, e.g. to persist a session obtained by a login helper. */
  get credentials(): { apiKey?: string; sessionToken?: string; bearerToken?: string } {
    const out: { apiKey?: string; sessionToken?: string; bearerToken?: string } = {};
    if (this.apiKey) out.apiKey = this.apiKey;
    if (this.sessionToken) out.sessionToken = this.sessionToken;
    if (this.bearerToken) out.bearerToken = this.bearerToken;
    return out;
  }

  // Transport

  /** Absolute URL for an API path; paths without /api/ are taken as /api/v1-relative. */
  url(path: string): string {
    if (/^https?:\/\//i.test(path)) {
      return path;
    }
    const normalized = path.startsWith('/') ? path : `/${path}`;
    return `${this.baseUrl}${normalized.startsWith('/api/') ? '' : '/api/v1'}${normalized}`;
  }

  /** The credential headers this client sends, e.g. for opening the WebSocket endpoints. */
  authHeaders(): Record<string, string> {
    const headers: Record<string, string> = {};
    if (this.apiKey) {
      headers['X-API-Key'] = this.apiKey;
    } else if (this.sessionToken) {
      headers['X-Session-Token'] = this.sessionToken;
    }
    if (this.bearerToken) {
      headers['Authorization'] = `Bearer ${this.bearerToken}`;
    }
    return headers;
  }

  private async send(method: string, path: string, body: BodyInit | undefined, contentType: string | undefined, auth: boolean): Promise<Response> {
    const headers: Record<string, string> = { ...this.headers };
    if (contentType) {
      headers['Content-Type'] = contentType;
    }
    if (auth) {
      Object.assign(headers, this.authHeaders());
    }
    const init: RequestInit = { method, headers };
    if (body !== undefined) {
      init.body = body;
    }
    if (this.timeoutMs > 0) {
      init.signal = AbortSignal.timeout(this.timeoutMs);
    }
    const response = await this.fetchImpl(this.url(path), init);
    if (!response.ok) {
      const text = await response.text();
      throw new ApiError(response.status, errorMessage(text), text.trim());
    }
    return response;
  }

  /**
   * Sends a JSON request and returns the decoded response (undefined for an
   * empty body). Use it for endpoints this client has no method for yet.
   */
  async request<T = unknown>(method: string, path: string, body?: unknown, options: { auth?: boolean } = {}): Promise<T> {
    const payload = body === undefined ? undefined : JSON.stringify(body);
    const response = await this.send(method, path, payload, payload === undefined ? undefined : 'application/json', options.auth ?? true);
    const text = await response.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  /** Sends a request without a body and returns the raw response bytes. */
  async requestBytes(method: string, path: string, options: { auth?: boolean } = {}): Promise<Uint8Array> {
    const response = await this.send(method, path, undefined, undefined, options.auth ?? true);
    return new Uint8Array(await response.arrayBuffer());
  }

  private async list<T>(path: string, key?: string): Promise<T[]> {
    let result: unknown = await this.request('GET', path);
    if (key !== undefined) {
      result = result && typeof result === 'object' && !Array.isArray(result) ? (result as Record<string, unknown>)[key] : undefined;
    }
    return Array.isArray(result) ? (result as T[]) : [];
  }

  private storeSession(result: LoginReply | undefined): void {
    if (result?.session_token) {
      this.sessionToken = result.session_token;
    }
    if (result?.access_token) {
      this.bearerToken = result.access_token;
    }
  }

  // Public: registration and login

  /** Addresses the gateway advertises to clients and agents. Needs no credentials. */
  getGatewayInfo(): Promise<GatewayInfo> {
    return this.request('GET', '/gateway-info', undefined, { auth: false });
  }

  /** The server's build version. Needs no credentials. */
  getVersion(): Promise<VersionInfo> {
    return this.request('GET', '/version', undefined, { auth: false });
  }

  /** The server's OpenAPI 3 document, as YAML text. */
  async getOpenApiSpec(): Promise<string> {
    return new TextDecoder().decode(await this.requestBytes('GET', '/openapi.yaml', { auth: false }));
  }

  /** Where to find the OpenAPI document (/openapi.json answers with pointers, not the spec). */
  getOpenApiIndex(): Promise<Record<string, unknown>> {
    return this.request('GET', '/openapi.json', undefined, { auth: false });
  }

  /** Registers an agent. The server requires an admin or operator, so credentials are sent. */
  registerAgent(req: RegisterAgentRequest): Promise<RegisterAgentResponse> {
    return this.request('POST', '/public/register/agent', req);
  }

  /** Registers a user. `is_admin` is honoured only for an admin caller or the first account. */
  registerClient(req: RegisterClientRequest): Promise<RegisterClientResponse> {
    return this.request('POST', '/public/register/client', req);
  }

  /** A one-time challenge for the SSH-key logins. */
  async issueChallenge(username: string): Promise<string> {
    const result = await this.request<{ challenge?: string } | undefined>('POST', '/public/auth/challenge', { username }, { auth: false });
    if (!result?.challenge) {
      throw new Error('no challenge returned');
    }
    return result.challenge;
  }

  private async signedLogin<T extends LoginReply>(path: string, username: string, signer: Signer, totpCode?: string): Promise<T> {
    const challenge = await this.issueChallenge(username);
    const { format, signature } = await signer.sign(new TextEncoder().encode(challenge));
    const body: Record<string, string> = {
      username,
      public_key: signer.publicKey,
      challenge,
      signature_format: format,
      signature: base64(signature),
    };
    const totp = totpCode?.trim();
    if (totp) {
      body['totp_code'] = totp;
    }
    return (await this.request<T | undefined>('POST', path, body, { auth: false })) ?? ({} as T);
  }

  /** SSH challenge login that returns a session token (POST /public/login). */
  async loginWithSshSession(username: string, signer: Signer, totpCode?: string): Promise<PasswordLoginResult> {
    const result = await this.signedLogin<PasswordLoginResult>('/public/login', username, signer, totpCode);
    if (!result.session_token) {
      throw new Error('no session token returned');
    }
    this.storeSession(result);
    return result;
  }

  /** SSH challenge login that returns an API key (POST /public/login/key). */
  async loginWithSshKey(username: string, signer: Signer, totpCode?: string): Promise<APIKeyLoginResult> {
    const result = await this.signedLogin<APIKeyLoginResult>('/public/login/key', username, signer, totpCode);
    if (!result.api_key) {
      throw new Error('no API key returned');
    }
    this.apiKey = result.api_key;
    return result;
  }

  /** SSH challenge login that returns a JWT bearer token (POST /public/login/token). */
  async loginWithSshJwt(username: string, signer: Signer, totpCode?: string): Promise<JWTLoginResult> {
    const result = await this.signedLogin<JWTLoginResult>('/public/login/token', username, signer, totpCode);
    if (!result.access_token) {
      throw new Error('no access token returned');
    }
    this.bearerToken = result.access_token;
    return result;
  }

  async loginWithPassword(username: string, password: string, totpCode = ''): Promise<PasswordLoginResult> {
    const result = (await this.request<PasswordLoginResult | undefined>(
      'POST', '/public/login/password', { username, password, totp_code: totpCode }, { auth: false },
    )) ?? {};
    if (!result.session_token) {
      throw new Error('no session token returned');
    }
    this.storeSession(result);
    return result;
  }

  async redeemBrowserBootstrap(code: string): Promise<PasswordLoginResult> {
    const result = (await this.request<PasswordLoginResult | undefined>('POST', '/public/auth/browser-bootstrap/redeem', { code }, { auth: false })) ?? {};
    this.storeSession(result);
    return result;
  }

  webauthnLoginBegin(username: string): Promise<Record<string, unknown>> {
    return this.request('POST', '/public/webauthn/login/begin', { username }, { auth: false });
  }

  async webauthnLoginFinish(username: string, response: Record<string, unknown>): Promise<PasswordLoginResult> {
    const result = (await this.request<PasswordLoginResult | undefined>('POST', '/public/webauthn/login/finish', { username, response }, { auth: false })) ?? {};
    this.storeSession(result);
    return result;
  }

  /** Calls a plugin's inbound webhook. The plugin authenticates the caller itself. */
  pluginWebhook<T = unknown>(plugin: string, path: string, method = 'POST', body?: unknown): Promise<T> {
    const sub = path.replace(/^\/+/, '').split('/').map(seg).join('/');
    return this.request<T>(method, `/public/plugins/${seg(plugin)}/${sub}`, body, { auth: false });
  }

  // Current user

  /**
   * Ends the HTTP session and drops the session and bearer tokens from this
   * client. The server does not revoke a JWT at logout: one issued alongside
   * the session stays valid until it expires.
   */
  async logout(): Promise<void> {
    await this.request('POST', '/logout');
    this.sessionToken = undefined;
    this.bearerToken = undefined;
  }

  getCurrentUser(): Promise<AuthUser> {
    return this.request('GET', '/auth/me');
  }

  /** A one-time code that opens a browser session as this user. */
  requestBrowserBootstrap(): Promise<BootstrapCode> {
    return this.request('POST', '/auth/browser-bootstrap');
  }

  async setPassword(password: string, totpCode: string): Promise<void> {
    await this.request('POST', '/auth/password', { password, totp_code: totpCode });
  }

  async clearPassword(totpCode: string): Promise<void> {
    await this.request('DELETE', '/auth/password', { totp_code: totpCode });
  }

  mfaEnroll(): Promise<MFAEnrollResponse> {
    return this.request('POST', '/mfa/enroll', {});
  }

  async mfaConfirm(code: string): Promise<void> {
    await this.request('POST', '/mfa/confirm', { code });
  }

  async mfaDisable(code: string): Promise<void> {
    await this.request('POST', '/mfa/disable', { code });
  }

  mfaStatus(): Promise<MFAStatus> {
    return this.request('GET', '/mfa/status');
  }

  webauthnRegisterBegin(): Promise<Record<string, unknown>> {
    return this.request('POST', '/webauthn/register/begin', {});
  }

  /** Completes registration and returns the new credential's ID. */
  async webauthnRegisterFinish(response: Record<string, unknown>): Promise<string> {
    const result = await this.request<{ id?: string } | undefined>('POST', '/webauthn/register/finish', response);
    return result?.id ?? '';
  }

  webauthnCredentials(): Promise<WebAuthnCredential[]> {
    return this.list('/webauthn/credentials');
  }

  async webauthnDeleteCredential(credentialId: string): Promise<void> {
    await this.request('DELETE', `/webauthn/credentials/${seg(credentialId)}`);
  }

  listSshKeys(): Promise<SSHKey[]> {
    return this.list('/ssh-keys');
  }

  addSshKey(name: string, publicKey: string): Promise<SSHKey> {
    return this.request('POST', '/ssh-keys', { name, public_key: publicKey });
  }

  async deleteSshKey(keyId: string): Promise<void> {
    await this.request('DELETE', `/ssh-keys/${seg(keyId)}`);
  }

  /** Creates a key; the raw key is in `api_key` and is returned only this once. */
  createApiKey(req: CreateAPIKeyRequest): Promise<CreateAPIKeyResponse> {
    return this.request('POST', '/api-keys', req);
  }

  listApiKeys(): Promise<APIKey[]> {
    return this.list('/api-keys', 'api_keys');
  }

  async revokeApiKey(keyId: string): Promise<void> {
    await this.request('POST', `/api-keys/${seg(keyId)}/revoke`);
  }

  async deleteApiKey(keyId: string): Promise<void> {
    await this.request('DELETE', `/api-keys/${seg(keyId)}`);
  }

  // Notifications

  listNotifications(limit = 0): Promise<Notification[]> {
    return this.list(`/notifications${query({ limit })}`);
  }

  async unreadNotificationCount(): Promise<number> {
    const result = await this.request<{ unread?: number } | undefined>('GET', '/notifications/unread-count');
    return result?.unread ?? 0;
  }

  async markNotificationRead(notificationId: string): Promise<void> {
    await this.request('POST', `/notifications/${seg(notificationId)}/read`);
  }

  async markAllNotificationsRead(): Promise<void> {
    await this.request('POST', '/notifications/read-all');
  }

  /** The caller's preferences and the admin bounds they were resolved within. */
  getNotificationPrefs(): Promise<NotificationPrefsResult> {
    return this.request('GET', '/notifications/prefs');
  }

  /** Stores preferences. Values outside the admin policy are clamped; `adjusted` says so. */
  putNotificationPrefs(prefs: NotificationPrefs): Promise<NotificationPrefsResult> {
    return this.request('PUT', '/notifications/prefs', prefs);
  }

  // Users, machines, permissions

  listUsers(): Promise<User[]> {
    return this.list('/users');
  }

  getUser(userId: string): Promise<User> {
    return this.request('GET', `/users/${seg(userId)}`);
  }

  createUser(req: CreateUserRequest): Promise<User> {
    return this.request('POST', '/admin/users', req);
  }

  updateUser(userId: string, req: UpdateUserRequest): Promise<User> {
    return this.request('PUT', `/admin/users/${seg(userId)}`, req);
  }

  async deleteUser(userId: string): Promise<void> {
    await this.request('DELETE', `/admin/users/${seg(userId)}`);
  }

  listMachines(): Promise<Machine[]> {
    return this.list('/machines');
  }

  getMachine(machineId: string): Promise<Machine> {
    return this.request('GET', `/machines/${seg(machineId)}`);
  }

  /** Resolves a machine by name from listMachines; throws when absent. */
  async getMachineByName(name: string): Promise<Machine> {
    const machine = (await this.listMachines()).find((m) => m.name === name);
    if (!machine) {
      throw new Error(`machine ${JSON.stringify(name)} not found`);
    }
    return machine;
  }

  createMachine(req: CreateMachineRequest): Promise<Machine> {
    return this.request('POST', '/admin/machines', req);
  }

  updateMachine(machineId: string, req: UpdateMachineRequest): Promise<Machine> {
    return this.request('PUT', `/admin/machines/${seg(machineId)}`, req);
  }

  /** Deletes a machine, or with `archive` deactivates it and keeps its history. */
  async deleteMachine(machineId: string, archive = false): Promise<void> {
    await this.request('DELETE', `/admin/machines/${seg(machineId)}${query({ archive })}`);
  }

  /** Reconciles a cluster's RoleBindings with its grants; `dryRun` only reports drift. */
  kubeReconcile(machineId: string, dryRun = false): Promise<KubeReconcileResult> {
    return this.request(dryRun ? 'GET' : 'POST', `/admin/machines/${seg(machineId)}/kubernetes/reconcile`);
  }

  getUserPermissions(userId: string): Promise<Permission[]> {
    return this.list(`/permissions/user/${seg(userId)}`);
  }

  getMachinePermissions(machineId: string): Promise<Permission[]> {
    return this.list(`/permissions/machine/${seg(machineId)}`);
  }

  listAllPermissions(limit = 0): Promise<Permission[]> {
    return this.list(`/admin/permissions${query({ limit })}`);
  }

  grantPermission(req: GrantPermissionRequest): Promise<Permission> {
    return this.request('POST', '/admin/permissions', req);
  }

  updatePermission(permissionId: string, req: UpdatePermissionRequest): Promise<Permission> {
    return this.request('PATCH', `/admin/permissions/${seg(permissionId)}`, req);
  }

  async revokePermission(permissionId: string): Promise<void> {
    await this.request('DELETE', `/admin/permissions/${seg(permissionId)}`);
  }

  /** Grants one access type on every user x machine pair. */
  bulkGrantPermissions(req: BulkGrantRequest): Promise<BulkResult> {
    return this.request('POST', '/admin/permissions/bulk', req);
  }

  bulkRevokePermissions(ids: string[]): Promise<BulkResult> {
    return this.request('POST', '/admin/permissions/bulk-revoke', { ids });
  }

  /** Resolved access for one user or one machine; set exactly one. */
  effectivePermissions(filter: { userId?: string; machineId?: string }): Promise<EffectiveAccess[]> {
    return this.list(`/admin/permissions/effective${query({ user_id: filter.userId, machine_id: filter.machineId })}`);
  }

  /** What the caller holds or could request. `scope` is "related" (default) or "all". */
  listCapabilities(scope = '', userId = ''): Promise<CapabilityCatalog> {
    return this.request('GET', `/capabilities${query({ scope, user_id: userId })}`);
  }

  /** Whether a user (default: the caller) may reach a machine as `remoteUser`, and why. */
  explainAccess(machine: string, remoteUser: string, accessType = '', user = ''): Promise<AccessExplanation> {
    return this.request('GET', `/access/explain${query({ machine, remote_user: remoteUser, access_type: accessType, user })}`);
  }

  // Access requests

  listAccessRequests(status = ''): Promise<AccessRequest[]> {
    return this.list(`/access-requests${query({ status })}`);
  }

  createAccessRequest(req: CreateAccessRequestRequest): Promise<AccessRequest> {
    return this.request('POST', '/access-requests', req);
  }

  getAccessRequest(requestId: string): Promise<AccessRequest> {
    return this.request('GET', `/access-requests/${seg(requestId)}`);
  }

  listPendingAccessRequests(): Promise<AccessRequest[]> {
    return this.list('/admin/access-requests/pending');
  }

  async approveAccessRequest(requestId: string, reviewerId = ''): Promise<void> {
    await this.request('POST', `/admin/access-requests/${seg(requestId)}/approve`, { reviewer_id: reviewerId });
  }

  async rejectAccessRequest(requestId: string, reviewerId = ''): Promise<void> {
    await this.request('POST', `/admin/access-requests/${seg(requestId)}/reject`, { reviewer_id: reviewerId });
  }

  getRiskPolicy(): Promise<RiskPolicy> {
    return this.request('GET', '/admin/risk-policy');
  }

  /** Replaces the risk policy outright; send the whole document. */
  putRiskPolicy(policy: RiskPolicy): Promise<RiskPolicy> {
    return this.request('PUT', '/admin/risk-policy', policy);
  }

  // Sessions, audit, reporting

  listSessions(status = ''): Promise<Session[]> {
    return this.list(`/sessions${query({ status })}`);
  }

  listActiveSessions(): Promise<Session[]> {
    return this.list('/sessions/active');
  }

  getSession(sessionId: string): Promise<Session> {
    return this.request('GET', `/sessions/${seg(sessionId)}`);
  }

  /** The raw session recording. */
  getSessionContent(sessionId: string): Promise<Uint8Array> {
    return this.requestBytes('GET', `/sessions/${seg(sessionId)}/content`);
  }

  listAuditLogs(limit = 0): Promise<AuditLog[]> {
    return this.list(`/audit-logs${query({ limit })}`);
  }

  /** Report bytes. `params` carries report options such as from/to or as_of. */
  exportReport(reportName: string, format = '', params: Record<string, string> = {}): Promise<Uint8Array> {
    return this.requestBytes('GET', `/reports/${seg(reportName)}/export${query({ ...params, format })}`);
  }

  getUsageDashboard(windowHours = 0, top = 0): Promise<UsageDashboard> {
    return this.request('GET', `/dashboard/usage${query({ window_hours: windowHours, top })}`);
  }

  listAnomalies(limit = 0, userId = ''): Promise<AccessAnomaly[]> {
    return this.list(`/anomalies${query({ limit, user_id: userId })}`);
  }

  getSetupStatus(): Promise<SetupStatus> {
    return this.request('GET', '/setup/status');
  }

  // Notification policy and templates (admin)

  getNotificationPolicy(): Promise<NotificationPolicyResult> {
    return this.request('GET', '/admin/notifications/policy');
  }

  /** Replaces the policy outright; send the whole document. */
  putNotificationPolicy(policy: NotificationPolicy): Promise<NotificationPolicy> {
    return this.request('PUT', '/admin/notifications/policy', policy);
  }

  listNotificationTemplates(): Promise<NotificationTemplateEntry[]> {
    return this.list('/admin/notifications/templates', 'templates');
  }

  putNotificationTemplate(event: string, template: NotificationTemplate): Promise<NotificationTemplate> {
    // The server rejects a body naming a different event than the path.
    return this.request('PUT', `/admin/notifications/templates/${seg(event)}`, { ...template, event_type: event });
  }

  /** Drops the override, returning the event to its built-in copy. */
  async deleteNotificationTemplate(event: string): Promise<void> {
    await this.request('DELETE', `/admin/notifications/templates/${seg(event)}`);
  }

  // Plugins and agents (admin)

  listPlugins(): Promise<PluginInfo[]> {
    return this.list('/admin/plugins', 'plugins');
  }

  /** Stores config. `configure_error` reports a config the plugin rejected after saving. */
  updatePluginConfig(name: string, enabled: boolean, config: Record<string, unknown>): Promise<PluginConfigResult> {
    return this.request('PUT', `/admin/plugins/${seg(name)}/config`, { enabled, config });
  }

  async enablePlugin(name: string): Promise<PluginInfo> {
    const result = await this.request<{ plugin?: PluginInfo } | undefined>('POST', `/admin/plugins/${seg(name)}/enable`);
    return result?.plugin ?? {};
  }

  async disablePlugin(name: string): Promise<PluginInfo> {
    const result = await this.request<{ plugin?: PluginInfo } | undefined>('POST', `/admin/plugins/${seg(name)}/disable`);
    return result?.plugin ?? {};
  }

  /** Machine IDs with a live agent connection. */
  listConnectedAgents(): Promise<string[]> {
    return this.list('/admin/agents/connected', 'agents');
  }

  /** Sends a control command (reload, drain, undrain, update) and returns its output. */
  async sendAgentCommand(machineId: string, command: string): Promise<string> {
    const result = await this.request<{ output?: string } | undefined>('POST', `/admin/agents/${seg(machineId)}/command`, { command });
    return result?.output ?? '';
  }

  async disconnectAgent(machineId: string): Promise<void> {
    await this.request('POST', `/admin/agents/${seg(machineId)}/disconnect`);
  }

  generateAgentInstallScript(req: AgentInstallScriptRequest): Promise<AgentInstallScriptResponse> {
    return this.request('POST', '/admin/agents/install-script', req);
  }

  // SSH certificate authority

  getTrustedCa(): Promise<TrustedCA> {
    return this.request('GET', '/ssh-cert/ca');
  }

  issueUserCert(publicKey: string, ttlHours = 0): Promise<IssuedCert> {
    return this.request('POST', '/ssh-cert', { public_key: publicKey, ttl_hours: ttlHours });
  }

  exportCa(): Promise<TrustedCA> {
    return this.request('GET', '/admin/ca/export');
  }

  listSshCertificates(certType = ''): Promise<SSHCertificate[]> {
    return this.list(`/admin/ssh-certificates${query({ cert_type: certType })}`, 'certificates');
  }

  async revokeSshCertificate(serial: string, reason = ''): Promise<void> {
    await this.request('POST', `/admin/ssh-certificates/${seg(serial)}/revoke`, { reason });
  }

  // Remote files

  /** Lists a directory; the server defaults to "/" as root. */
  async listFiles(machine: string, path = '', remoteUser = ''): Promise<FileListResponse> {
    const result = (await this.request<FileListResponse | undefined>('GET', `/files/list${query({ machine, path, user: remoteUser })}`)) ?? {};
    result.entries ??= [];
    return result;
  }

  downloadFile(machine: string, path: string, remoteUser = ''): Promise<Uint8Array> {
    return this.requestBytes('GET', `/files/download${query({ machine, path, user: remoteUser })}`);
  }

  async uploadFile(machine: string, path: string, fileName: string, content: Blob | Uint8Array | ArrayBuffer, remoteUser = ''): Promise<FileUploadResponse> {
    const form = new FormData();
    form.set('machine', machine);
    form.set('path', path);
    if (remoteUser) {
      form.set('user', remoteUser);
    }
    const blob = content instanceof Blob ? content : new Blob([content as BlobPart]);
    form.set('file', blob, fileName);
    // fetch sets the multipart Content-Type with its boundary.
    const response = await this.send('POST', '/files/upload', form, undefined, true);
    const text = await response.text();
    return (text ? JSON.parse(text) : {}) as FileUploadResponse;
  }

  async makeDir(machine: string, path: string, remoteUser = ''): Promise<void> {
    const body: Record<string, string> = { machine, path };
    if (remoteUser) {
      body['user'] = remoteUser;
    }
    await this.request('POST', '/files/mkdir', body);
  }

  async deleteFile(machine: string, path: string, remoteUser = ''): Promise<void> {
    await this.request('DELETE', `/files${query({ machine, path, user: remoteUser })}`);
  }

  // WebSocket endpoints. These return the ws:// or wss:// URL; open it with a
  // WebSocket client that can send authHeaders() on the upgrade request.

  private wsUrl(path: string): string {
    return this.url(path).replace(/^http/i, 'ws');
  }

  /** URL of an interactive web terminal on `machine`. */
  terminalWsUrl(machine: string, remoteUser = ''): string {
    return this.wsUrl(`/terminal/ws${query({ machine, user: remoteUser })}`);
  }

  /** URL that streams a live session's output to a watcher. */
  sessionWatchWsUrl(sessionId: string): string {
    return this.wsUrl(`/sessions/${seg(sessionId)}/watch`);
  }
}
