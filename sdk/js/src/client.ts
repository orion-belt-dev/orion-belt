import {
  ApiError,
  AuthMeResponse,
  ClientOptions,
  ChallengeResponse,
  FileListResponse,
  FileUploadResponse,
  JWTLoginResponse,
  LoginResponse,
  LoginWithKeyResponse,
  Machine,
  PasswordLoginResponse,
  Session,
  UsageDashboard,
} from './types.js';

const defaultTimeoutMs = 30_000;

function withTimeout(timeoutMs: number): AbortSignal | undefined {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    return undefined;
  }
  return AbortSignal.timeout(timeoutMs);
}

function trimBaseUrl(baseUrl: string): string {
  const trimmed = baseUrl.trim();
  if (!trimmed) {
    throw new Error('baseUrl is required');
  }
  return trimmed.replace(/\/+$/, '');
}

function buildUrl(baseUrl: string, path: string): string {
  if (/^https?:\/\//i.test(path)) {
    return path;
  }
  const normalized = path.startsWith('/') ? path : `/${path}`;
  return `${baseUrl}${normalized.startsWith('/api/') ? '' : '/api/v1'}${normalized}`;
}

function encodeQuery(params: Record<string, string | number | boolean | undefined>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') {
      continue;
    }
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : '';
}

function isBodyInit(value: unknown): value is BodyInit {
  return value instanceof FormData || value instanceof Blob || value instanceof URLSearchParams || value instanceof ArrayBuffer;
}

export class OrionBeltClient {
  private baseUrl: string;
  private timeoutMs: number;
  private apiKey: string | undefined;
  private sessionToken: string | undefined;
  private bearerToken: string | undefined;
  private defaultHeaders: HeadersInit;

  constructor(options: ClientOptions) {
    this.baseUrl = trimBaseUrl(options.baseUrl);
    this.timeoutMs = options.timeoutMs ?? defaultTimeoutMs;
    this.apiKey = options.apiKey?.trim() || undefined;
    this.sessionToken = options.sessionToken?.trim() || undefined;
    this.bearerToken = options.bearerToken?.trim() || undefined;
    this.defaultHeaders = options.headers ?? {};
  }

  setApiKey(apiKey?: string): void {
    this.apiKey = apiKey?.trim() || undefined;
  }

  setSessionToken(sessionToken?: string): void {
    this.sessionToken = sessionToken?.trim() || undefined;
  }

  setBearerToken(bearerToken?: string): void {
    this.bearerToken = bearerToken?.trim() || undefined;
  }

  private buildHeaders(contentType?: string): Headers {
    const headers = new Headers(this.defaultHeaders);
    if (contentType) {
      headers.set('Content-Type', contentType);
    }
    if (this.apiKey) {
      headers.set('X-API-Key', this.apiKey);
    } else if (this.sessionToken) {
      headers.set('X-Session-Token', this.sessionToken);
    }
    if (this.bearerToken) {
      headers.set('Authorization', `Bearer ${this.bearerToken}`);
    }
    return headers;
  }

  private async requestJson<T>(method: string, path: string, body?: unknown, auth = true): Promise<T> {
    const headers = auth ? this.buildHeaders() : new Headers(this.defaultHeaders);
    const isMultipartBody = body !== undefined && isBodyInit(body);
    if (!isMultipartBody) {
      headers.set('Content-Type', 'application/json');
    }

    const init: RequestInit = {
      method,
      headers,
    };
    const signal = withTimeout(this.timeoutMs);
    if (signal) {
      init.signal = signal;
    }
    if (body !== undefined) {
      init.body = isMultipartBody ? body : JSON.stringify(body);
    }

    const response = await fetch(buildUrl(this.baseUrl, path), init);
    const text = await response.text();

    if (!response.ok) {
      let message = text.trim();
      try {
        const parsed = JSON.parse(text) as { error?: string; message?: string };
        message = parsed.error || parsed.message || message;
      } catch {
        // keep raw text
      }
      throw new ApiError({ statusCode: response.status, message, body: text });
    }

    if (!text) {
      return undefined as T;
    }

    return JSON.parse(text) as T;
  }

  private async requestBytes(method: string, path: string, body?: BodyInit, auth = true): Promise<Uint8Array> {
    const headers = auth ? this.buildHeaders() : new Headers(this.defaultHeaders);
    const init: RequestInit = {
      method,
      headers,
    };
    const signal = withTimeout(this.timeoutMs);
    if (signal) {
      init.signal = signal;
    }
    if (body !== undefined) {
      init.body = body;
    }

    const response = await fetch(buildUrl(this.baseUrl, path), init);
    const bytes = new Uint8Array(await response.arrayBuffer());
    if (!response.ok) {
      throw new ApiError({ statusCode: response.status, message: new TextDecoder().decode(bytes).trim(), body: new TextDecoder().decode(bytes) });
    }
    return bytes;
  }

  async issueChallenge(username: string): Promise<string> {
    const result = await this.requestJson<ChallengeResponse>('POST', '/public/auth/challenge', { username }, false);
    return result.challenge;
  }

  async loginWithPassword(username: string, password: string, totpCode: string): Promise<PasswordLoginResponse> {
    const result = await this.requestJson<PasswordLoginResponse>('POST', '/public/login/password', { username, password, totp_code: totpCode }, false);
    this.sessionToken = result.session_token;
    if (result.access_token) {
      this.bearerToken = result.access_token;
    }
    return result;
  }

  async loginWithKey(payload: {
    username: string;
    public_key: string;
    challenge: string;
    signature_format: string;
    signature: string;
    totp_code?: string;
  }): Promise<LoginWithKeyResponse> {
    const result = await this.requestJson<LoginWithKeyResponse>('POST', '/public/login/key', payload, false);
    this.apiKey = result.api_key;
    return result;
  }

  async loginWithJwt(payload: {
    username: string;
    public_key: string;
    challenge: string;
    signature_format: string;
    signature: string;
    totp_code?: string;
  }): Promise<JWTLoginResponse> {
    const result = await this.requestJson<JWTLoginResponse>('POST', '/public/login/token', payload, false);
    this.bearerToken = result.access_token;
    return result;
  }

  async getCurrentUser(): Promise<AuthMeResponse> {
    return this.requestJson<AuthMeResponse>('GET', '/auth/me');
  }

  async listMachines(): Promise<Machine[]> {
    return this.requestJson<Machine[]>('GET', '/machines');
  }

  async listSessions(status?: string): Promise<Session[]> {
    return this.requestJson<Session[]>('GET', `/sessions${encodeQuery({ status })}`);
  }

  async getUsageDashboard(windowHours = 24, top = 10): Promise<UsageDashboard> {
    return this.requestJson<UsageDashboard>('GET', `/dashboard/usage${encodeQuery({ window_hours: windowHours, top })}`);
  }

  async exportReport(reportName: string, format = 'csv'): Promise<Uint8Array> {
    return this.requestBytes('GET', `/reports/${encodeURIComponent(reportName)}/export${encodeQuery({ format })}`);
  }

  async listFiles(machine: string, path = '/', remoteUser = 'root'): Promise<FileListResponse> {
    return this.requestJson<FileListResponse>('GET', `/files/list${encodeQuery({ machine, path, user: remoteUser })}`);
  }

  async downloadFile(machine: string, path: string, remoteUser = 'root'): Promise<Uint8Array> {
    return this.requestBytes('GET', `/files/download${encodeQuery({ machine, path, user: remoteUser })}`);
  }

  async uploadFile(machine: string, path: string, fileName: string, content: Blob | Uint8Array | ArrayBuffer, remoteUser = 'root'): Promise<FileUploadResponse> {
    const formData = new FormData();
    formData.set('machine', machine);
    formData.set('path', path);
    formData.set('user', remoteUser);
    const file = content instanceof Blob ? content : new Blob([content]);
    formData.set('file', file, fileName);
    return this.requestJson<FileUploadResponse>('POST', '/files/upload', formData, true);
  }
}

export { ApiError } from './types.js';