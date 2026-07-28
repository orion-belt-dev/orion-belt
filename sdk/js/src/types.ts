export type AuthHeaders = {
  apiKey?: string;
  sessionToken?: string;
  bearerToken?: string;
};

export type ClientOptions = {
  baseUrl: string;
  timeoutMs?: number;
  headers?: HeadersInit;
} & AuthHeaders;

export type ApiErrorShape = {
  statusCode: number;
  message: string;
  body: string;
};

export class ApiError extends Error {
  statusCode: number;
  body: string;

  constructor(error: ApiErrorShape) {
    super(error.message || `api error (${error.statusCode})`);
    this.name = 'ApiError';
    this.statusCode = error.statusCode;
    this.body = error.body;
  }
}

export type ChallengeResponse = {
  challenge: string;
};

export type LoginUser = {
  id: string;
  username: string;
  email: string;
  is_admin: boolean;
  role?: string;
  mfa_enabled?: boolean;
  password_set?: boolean;
  must_set_password?: boolean;
};

export type LoginResponse = {
  session_token: string;
  access_token?: string;
  expires_at: string;
  user: LoginUser;
};

export type LoginWithKeyResponse = {
  api_key: string;
  expires_at?: string;
  user: {
    id: string;
    username: string;
    email: string;
    is_admin: boolean;
  };
};

export type JWTLoginResponse = {
  access_token: string;
  token_type: string;
  expires_at: string;
  user: {
    id: string;
    username: string;
    email: string;
    is_admin: boolean;
    mfa_enabled: boolean;
  };
};

export type PasswordLoginResponse = LoginResponse;

export type AuthMeResponse = {
  id: string;
  username: string;
  email: string;
  public_key: string;
  is_admin: boolean;
  role: string;
  mfa_enabled: boolean;
  webauthn_enabled: boolean;
  password_set: boolean;
  must_set_password: boolean;
  created_at?: string;
  updated_at?: string;
};

export type Machine = {
  id: string;
  name: string;
  hostname: string;
  port: number;
  agent_id?: string;
  is_active?: boolean;
  tags?: Record<string, string>;
};

export type Session = {
  id: string;
  user_id: string;
  machine_id: string;
  remote_user: string;
  source: string;
  start_time: string;
  end_time?: string | null;
  recording_path?: string;
  status: string;
  created_at?: string;
};

export type UsageDashboard = {
  window_hours: number;
  from: string;
  to: string;
  generated_at: string;
  access_volume: {
    sessions_total: number;
    sessions_active: number;
    requests_total: number;
    requests_pending: number;
    requests_approved: number;
    requests_rejected: number;
  };
  approval_latency: {
    sample_size: number;
    average_seconds: number;
    p50_seconds: number;
    p95_seconds: number;
  };
  top_targets: Array<{
    machine_id: string;
    machine_name: string;
    session_count: number;
  }>;
};

export type FileEntry = {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mtime: number;
};

export type FileListResponse = {
  path: string;
  entries: FileEntry[];
  raw?: string;
};

export type FileUploadResponse = {
  message: string;
  path: string;
  size: number;
};