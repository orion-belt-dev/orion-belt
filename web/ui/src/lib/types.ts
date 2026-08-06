export type Role = "admin" | "operator" | "auditor" | "user";

export type User = {
  id: string;
  username: string;
  email: string;
  is_admin?: boolean;
  role?: string;
  mfa_enabled?: boolean;
  webauthn_enabled?: boolean;
  password_set?: boolean;
  must_set_password?: boolean;
  public_key?: string;
};

export type Machine = {
  id: string;
  name: string;
  hostname: string;
  port: number;
  is_active?: boolean;
  tags?: Record<string, string>;
  agent_id?: string;
  last_seen_at?: string | null;
};

export type Session = {
  id: string;
  user_id: string;
  machine_id: string;
  remote_user?: string;
  source?: string;
  start_time?: string;
  end_time?: string | null;
  recording_path?: string;
  status?: string;
};

export type AccessRequest = {
  id: string;
  user_id: string;
  machine_id: string;
  remote_users?: string[];
  access_type?: string;
  reason?: string;
  status?: string;
  duration?: number;
  requested_at?: string;
  reviewed_at?: string | null;
  created_at?: string;
  expires_at?: string | null;
};

// What the logged-in user could ask for, and why it is being offered.
export type CapabilityStatus = "granted" | "pending" | "requestable";
export type CapabilitySource = "grant" | "request" | "session" | "tag" | "directory";

export type Capability = {
  id: string;
  machine_id: string;
  machine_name: string;
  hostname?: string;
  port?: number;
  machine_active: boolean;
  tags?: Record<string, string>;
  remote_user: string; // "" means any login the approver allows
  access_type: string;
  status: CapabilityStatus;
  source: CapabilitySource;
  reason: string;
  permission_id?: string;
  request_id?: string;
  expires_at?: string | null;
  last_used_at?: string | null;
};

export type CapabilityCatalog = {
  user_id: string;
  scope: "related" | "all";
  generated_at: string;
  truncated?: boolean;
  capabilities: Capability[];
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

export type AuditLog = {
  id: string;
  user_id?: string;
  action: string;
  resource?: string;
  ip_address?: string;
  timestamp?: string;
  metadata?: Record<string, unknown>;
};

export type PluginConfigField = {
  key: string;
  label: string;
  type: "string" | "bool" | "int" | "object";
  secret?: boolean;
  required?: boolean;
  placeholder?: string;
  help?: string;
  fields?: PluginConfigField[]; // present when type === "object"
};

export type PluginInfo = {
  name: string;
  version: string;
  enabled: boolean;
  configured: boolean;
  last_error?: string;
  config: Record<string, unknown>;
  has_webhook: boolean;
  schema?: PluginConfigField[];
};

export type VersionInfo = {
  version?: string;
  commit?: string;
  date?: string;
  display?: string;
};

export type Permission = {
  id: string;
  user_id: string;
  machine_id: string;
  access_type: string;
  remote_users?: string[];
  granted_by?: string;
  granted_at?: string;
  expires_at?: string | null;
};

export type Notification = {
  id: string;
  user_id: string;
  type: string;
  title: string;
  body?: string;
  metadata?: Record<string, unknown>;
  read_at?: string | null;
  created_at?: string;
};

export class ApiError extends Error {
  status: number;
  data: unknown;
  constructor(message: string, status: number, data: unknown) {
    super(message);
    this.status = status;
    this.data = data;
  }
}
