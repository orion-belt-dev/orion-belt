// Response and request shapes for the Orion Belt API, kept in step with
// sdk/python/src/orion_belt_sdk/types.py. Timestamps are the RFC 3339 strings
// the server sends. Fields are optional because the server omits empty
// optional fields; request fields left out are not sent, which is how the
// update endpoints tell "unchanged" from "cleared".

export type LoginUser = {
  id?: string;
  username?: string;
  email?: string;
  is_admin?: boolean;
  role?: string;
  mfa_enabled?: boolean;
  password_set?: boolean;
  must_set_password?: boolean;
};

export type PasswordLoginResult = {
  session_token?: string;
  access_token?: string;
  expires_at?: string;
  user?: LoginUser;
};

export type APIKeyLoginResult = {
  api_key?: string;
  expires_at?: string;
  user?: LoginUser;
};

export type JWTLoginResult = {
  access_token?: string;
  token_type?: string;
  expires_at?: string;
  user?: LoginUser;
};

export type AuthUser = {
  id?: string;
  username?: string;
  email?: string;
  public_key?: string;
  is_admin?: boolean;
  role?: string;
  mfa_enabled?: boolean;
  webauthn_enabled?: boolean;
  password_set?: boolean;
  must_set_password?: boolean;
  created_at?: string;
  updated_at?: string;
};

export type User = {
  id?: string;
  username?: string;
  email?: string;
  public_key?: string;
  is_admin?: boolean;
  role?: string;
  mfa_enabled?: boolean;
  webauthn_enabled?: boolean;
  created_at?: string;
  updated_at?: string;
};

export type Machine = {
  id?: string;
  name?: string;
  hostname?: string;
  port?: number;
  tags?: Record<string, string>;
  agent_id?: string;
  is_active?: boolean;
  last_seen_at?: string;
  created_at?: string;
  updated_at?: string;
};

export type RiskSignals = {
  source_ip?: string;
  device?: string;
  auth_method?: string;
  mfa_enrolled?: boolean;
  hour_utc?: number;
  weekday?: string;
  target_sensitivity?: string;
  collected_at?: string;
};

export type RiskDecision = {
  outcome?: string;
  reasons?: string[];
};

export type AccessRequest = {
  id?: string;
  user_id?: string;
  machine_id?: string;
  remote_users?: string[];
  access_type?: string;
  reason?: string;
  duration?: number;
  status?: string;
  requested_at?: string;
  reviewed_at?: string;
  reviewed_by?: string;
  expires_at?: string;
  risk_signals?: RiskSignals;
  risk_decision?: RiskDecision;
};

export type Session = {
  id?: string;
  user_id?: string;
  machine_id?: string;
  remote_user?: string;
  source?: string;
  start_time?: string;
  end_time?: string;
  recording_path?: string;
  status?: string;
  created_at?: string;
};

export type AuditLog = {
  id?: string;
  user_id?: string;
  action?: string;
  resource?: string;
  metadata?: Record<string, unknown>;
  ip_address?: string;
  timestamp?: string;
};

export type Notification = {
  id?: string;
  user_id?: string;
  type?: string;
  title?: string;
  body?: string;
  metadata?: Record<string, unknown>;
  read_at?: string;
  created_at?: string;
};

export type NotificationPrefs = {
  user_id?: string;
  in_app_enabled?: boolean;
  email_enabled?: boolean;
  event_types?: string[];
};

export type NotificationPrefsResult = NotificationPrefs & {
  allowed_channels?: string[];
  mandatory_events?: Record<string, string[]>;
  adjusted?: boolean;
};

export type NotificationPolicy = {
  allowed_channels?: string[];
  mandatory_events?: Record<string, string[]>;
  updated_at?: string;
};

export type NotificationPolicyResult = {
  policy?: NotificationPolicy;
  known_channels?: string[];
  known_events?: string[];
};

export type NotificationTemplate = {
  event_type?: string;
  title?: string;
  body?: string;
  updated_at?: string;
};

export type NotificationTemplateEntry = NotificationTemplate & {
  customized?: boolean;
  default_title?: string;
  default_body?: string;
  placeholders?: string[];
};

export type HourRange = {
  start: number;
  end: number;
};

export type AutoApproveRule = {
  enabled?: boolean;
  max_sensitivity?: string;
  max_duration_seconds?: number;
  hours_utc?: HourRange;
  source_cidrs?: string[];
  require_mfa?: boolean;
  remote_users?: string[];
};

export type EscalateRule = {
  enabled?: boolean;
  sensitivities?: string[];
  outside_hours_utc?: HourRange;
  without_mfa?: boolean;
  source_outside_cidrs?: string[];
};

export type RiskPolicy = {
  auto_approve?: AutoApproveRule;
  escalate?: EscalateRule;
  updated_at?: string;
  updated_by?: string;
};

export type AccessAnomaly = {
  id?: string;
  kind?: string;
  signal?: string;
  user_id?: string;
  machine_id?: string;
  machine?: string;
  remote_user?: string;
  session_id?: string;
  ip_address?: string;
  flagged_at?: string;
};

export type AccessLink = {
  relation?: string;
  ref?: string;
  holds?: boolean;
  detail?: string;
  via?: string;
  reason?: string;
  granted_by?: string;
  at?: string;
  expires_at?: string;
};

export type AccessExplanation = {
  allowed?: boolean;
  summary?: string;
  user_id?: string;
  username?: string;
  machine_id?: string;
  machine?: string;
  access_type?: string;
  remote_user?: string;
  chain?: AccessLink[];
};

export type APIKey = {
  id?: string;
  user_id?: string;
  name?: string;
  key_prefix?: string;
  last_used_at?: string;
  expires_at?: string;
  created_at?: string;
  revoked_at?: string;
};

export type CreateAPIKeyResponse = {
  id?: string;
  name?: string;
  api_key?: string;
  key_prefix?: string;
  expires_at?: string;
  created_at?: string;
};

export type Permission = {
  id?: string;
  user_id?: string;
  machine_id?: string;
  access_type?: string;
  remote_users?: string[];
  granted_by?: string;
  granted_at?: string;
  expires_at?: string;
};

export type EffectiveAccess = {
  user_id?: string;
  username?: string;
  machine_id?: string;
  machine_name?: string;
  access_type?: string;
  remote_users?: string[];
  source?: string;
  expires_at?: string;
};

export type BulkFailure = {
  user_id?: string;
  machine_id?: string;
  permission_id?: string;
  error?: string;
};

export type BulkResult = {
  granted?: number;
  revoked?: number;
  failed?: BulkFailure[];
};

export type Capability = {
  id?: string;
  machine_id?: string;
  machine_name?: string;
  hostname?: string;
  port?: number;
  machine_active?: boolean;
  tags?: Record<string, string>;
  remote_user?: string;
  access_type?: string;
  status?: string;
  source?: string;
  reason?: string;
  permission_id?: string;
  request_id?: string;
  expires_at?: string;
  last_used_at?: string;
};

export type CapabilityCatalog = {
  user_id?: string;
  scope?: string;
  generated_at?: string;
  truncated?: boolean;
  capabilities?: Capability[];
};

export type SetupSteps = {
  admin_exists?: boolean;
  has_machines?: boolean;
  has_connected_agents?: boolean;
  has_users?: boolean;
  has_permissions?: boolean;
};

export type SetupCounts = {
  admins?: number;
  users?: number;
  machines?: number;
  connected_agents?: number;
  permissions?: number;
};

export type SetupStatus = {
  complete?: boolean;
  steps?: SetupSteps;
  counts?: SetupCounts;
  next?: string;
};

export type AccessVolume = {
  sessions_total?: number;
  sessions_active?: number;
  requests_total?: number;
  requests_pending?: number;
  requests_approved?: number;
  requests_rejected?: number;
};

export type ApprovalLatency = {
  sample_size?: number;
  average_seconds?: number;
  p50_seconds?: number;
  p95_seconds?: number;
};

export type TopTarget = {
  machine_id?: string;
  machine_name?: string;
  session_count?: number;
};

export type UsageDashboard = {
  window_hours?: number;
  from?: string;
  to?: string;
  generated_at?: string;
  access_volume?: AccessVolume;
  approval_latency?: ApprovalLatency;
  top_targets?: TopTarget[];
};

export type GatewayInfo = {
  public_url?: string;
  ui_url?: string;
  ssh_host?: string;
  ssh_port?: number;
  api_port?: number;
  public_ssh_host?: string;
  public_ssh_port?: number;
};

export type VersionInfo = {
  version?: string;
  commit?: string;
  date?: string;
  display?: string;
};

export type TrustedCA = {
  enabled?: boolean;
  user_ca?: string;
  host_ca?: string;
};

export type IssuedCert = {
  certificate?: string;
  serial?: string;
  expires_at?: string;
};

export type SSHCertificate = {
  id?: string;
  serial?: string;
  cert_type?: string;
  subject_id?: string;
  key_id?: string;
  principals?: string[];
  public_key_fingerprint?: string;
  issued_at?: string;
  expires_at?: string;
  revoked_at?: string;
  revoked_by?: string;
  revoke_reason?: string;
};

export type SSHKey = {
  id?: string;
  user_id?: string;
  name?: string;
  public_key?: string;
  key_type?: string;
  created_at?: string;
};

export type BootstrapCode = {
  code?: string;
  expires_at?: string;
};

export type PluginConfigField = {
  key?: string;
  label?: string;
  type?: string;
  secret?: boolean;
  required?: boolean;
  placeholder?: string;
  help?: string;
  fields?: PluginConfigField[];
};

export type PluginInfo = {
  name?: string;
  version?: string;
  enabled?: boolean;
  configured?: boolean;
  last_error?: string;
  config?: Record<string, unknown>;
  has_webhook?: boolean;
  schema?: PluginConfigField[];
};

export type PluginConfigResult = {
  plugin?: PluginInfo;
  configure_error?: string;
};

export type AgentInstallScriptResponse = {
  script?: string;
  machine_id?: string;
  user_id?: string;
  agent_name?: string;
  public_key?: string;
  filename?: string;
  message?: string;
  unsigned_build?: boolean;
};

export type RegisterAgentResponse = {
  user_id?: string;
  machine_id?: string;
  message?: string;
  host_certificate?: string;
  host_ca_public_key?: string;
};

export type RegisterClientResponse = {
  user_id?: string;
  message?: string;
};

export type MFAEnrollResponse = {
  secret?: string;
  otpauth_url?: string;
  backup_codes?: string[];
  message?: string;
};

export type MFAStatus = {
  mfa_enabled?: boolean;
  mfa_required?: boolean;
};

export type WebAuthnCredential = {
  id?: string;
  name?: string;
  created_at?: string;
  cred_id?: string;
};

export type FileEntry = {
  name?: string;
  path?: string;
  is_dir?: boolean;
  size?: number;
  mtime?: number;
};

export type FileListResponse = {
  path?: string;
  entries?: FileEntry[];
  raw?: string;
};

export type FileUploadResponse = {
  message?: string;
  path?: string;
  size?: number;
};

export type KubeBinding = {
  name?: string;
  namespace?: string;
  cluster_role?: string;
  subject?: string;
  user?: string;
  permission_id?: string;
  expires_at?: string;
};

export type KubePlan = {
  create?: KubeBinding[];
  update?: KubeBinding[];
  delete?: KubeBinding[];
};

export type KubeReconcileResult = {
  machine_id?: string;
  dry_run?: boolean;
  in_sync?: boolean;
  plan?: KubePlan;
};

export type CreateAPIKeyRequest = {
  name?: string;
  /** Lifetime in DAYS, not seconds. Leave it out for a key that never expires. */
  expires_in?: number;
};

export type CreateAccessRequestRequest = {
  machine_id?: string;
  reason?: string;
  /** Seconds. */
  duration?: number;
  access_type?: string;
  remote_users?: string[];
};

export type CreateUserRequest = {
  username?: string;
  email?: string;
  public_key?: string;
  role?: string;
  is_admin?: boolean;
};

export type UpdateUserRequest = {
  email?: string;
  public_key?: string;
  is_admin?: boolean;
  role?: string;
};

export type CreateMachineRequest = {
  name?: string;
  hostname?: string;
  port?: number;
  tags?: Record<string, string>;
  agent_id?: string;
  is_active?: boolean;
};

export type UpdateMachineRequest = {
  name?: string;
  hostname?: string;
  port?: number;
  tags?: Record<string, string>;
  agent_id?: string;
  is_active?: boolean;
};

export type GrantPermissionRequest = {
  user_id?: string;
  machine_id?: string;
  access_type?: string;
  remote_users?: string[];
  expires_at?: string;
  duration_seconds?: number;
};

export type UpdatePermissionRequest = {
  access_type?: string;
  remote_users?: string[];
  expires_at?: string;
  duration_seconds?: number;
};

export type BulkGrantRequest = {
  user_ids?: string[];
  machine_ids?: string[];
  access_type?: string;
  remote_users?: string[];
  expires_at?: string;
  duration_seconds?: number;
};

export type AgentInstallScriptRequest = {
  name?: string;
  hostname?: string;
  port?: number;
  tags?: Record<string, string>;
  os?: string;
  gateway_host?: string;
  gateway_port?: number;
  package_base_url?: string;
  version?: string;
  /** The server audits it. */
  allow_unsigned_build?: boolean;
};

export type RegisterAgentRequest = {
  name?: string;
  hostname?: string;
  port?: number;
  public_key?: string;
  tags?: Record<string, string>;
};

export type RegisterClientRequest = {
  username?: string;
  email?: string;
  public_key?: string;
  is_admin?: boolean;
};

