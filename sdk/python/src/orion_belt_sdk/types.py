"""Response and request shapes for the Orion Belt API.

These are TypedDicts rather than classes so a field the server adds later
reaches the caller instead of breaking deserialisation. Timestamps are the
RFC 3339 strings the server sends. Every key is optional (total=False)
because the server omits empty optional fields.
"""

from __future__ import annotations

from typing import Any, TypedDict


class LoginUser(TypedDict, total=False):
    id: str
    username: str
    email: str
    is_admin: bool
    role: str
    mfa_enabled: bool
    password_set: bool
    must_set_password: bool


class PasswordLoginResult(TypedDict, total=False):
    session_token: str
    access_token: str
    expires_at: str
    user: LoginUser


class APIKeyLoginResult(TypedDict, total=False):
    api_key: str
    expires_at: str
    user: LoginUser


class JWTLoginResult(TypedDict, total=False):
    access_token: str
    token_type: str
    expires_at: str
    user: LoginUser


class AuthUser(TypedDict, total=False):
    id: str
    username: str
    email: str
    public_key: str
    is_admin: bool
    role: str
    mfa_enabled: bool
    webauthn_enabled: bool
    password_set: bool
    must_set_password: bool
    created_at: str
    updated_at: str


class User(TypedDict, total=False):
    id: str
    username: str
    email: str
    public_key: str
    is_admin: bool
    role: str
    mfa_enabled: bool
    webauthn_enabled: bool
    created_at: str
    updated_at: str


class Machine(TypedDict, total=False):
    id: str
    name: str
    hostname: str
    port: int
    tags: dict[str, str]
    agent_id: str
    is_active: bool
    last_seen_at: str
    created_at: str
    updated_at: str


class RiskSignals(TypedDict, total=False):
    source_ip: str
    device: str
    auth_method: str
    mfa_enrolled: bool
    hour_utc: int
    weekday: str
    target_sensitivity: str
    collected_at: str


class RiskDecision(TypedDict, total=False):
    outcome: str
    reasons: list[str]


class AccessRequest(TypedDict, total=False):
    id: str
    user_id: str
    machine_id: str
    remote_users: list[str]
    access_type: str
    reason: str
    duration: int
    status: str
    requested_at: str
    reviewed_at: str
    reviewed_by: str
    expires_at: str
    risk_signals: RiskSignals
    risk_decision: RiskDecision


class Session(TypedDict, total=False):
    id: str
    user_id: str
    machine_id: str
    remote_user: str
    source: str
    start_time: str
    end_time: str
    recording_path: str
    status: str
    created_at: str


class AuditLog(TypedDict, total=False):
    id: str
    user_id: str
    action: str
    resource: str
    metadata: dict[str, Any]
    ip_address: str
    timestamp: str


class Notification(TypedDict, total=False):
    id: str
    user_id: str
    type: str
    title: str
    body: str
    metadata: dict[str, Any]
    read_at: str
    created_at: str


class NotificationPrefs(TypedDict, total=False):
    user_id: str
    in_app_enabled: bool
    email_enabled: bool
    event_types: list[str]


class NotificationPrefsResult(NotificationPrefs, total=False):
    allowed_channels: list[str]
    mandatory_events: dict[str, list[str]]
    adjusted: bool


class NotificationPolicy(TypedDict, total=False):
    allowed_channels: list[str]
    mandatory_events: dict[str, list[str]]
    updated_at: str


class NotificationPolicyResult(TypedDict, total=False):
    policy: NotificationPolicy
    known_channels: list[str]
    known_events: list[str]


class NotificationTemplate(TypedDict, total=False):
    event_type: str
    title: str
    body: str
    updated_at: str


class NotificationTemplateEntry(NotificationTemplate, total=False):
    customized: bool
    default_title: str
    default_body: str
    placeholders: list[str]


class HourRange(TypedDict):
    start: int
    end: int


class AutoApproveRule(TypedDict, total=False):
    enabled: bool
    max_sensitivity: str
    max_duration_seconds: int
    hours_utc: HourRange
    source_cidrs: list[str]
    require_mfa: bool
    remote_users: list[str]


class EscalateRule(TypedDict, total=False):
    enabled: bool
    sensitivities: list[str]
    outside_hours_utc: HourRange
    without_mfa: bool
    source_outside_cidrs: list[str]


class RiskPolicy(TypedDict, total=False):
    auto_approve: AutoApproveRule
    escalate: EscalateRule
    updated_at: str
    updated_by: str


class AccessAnomaly(TypedDict, total=False):
    id: str
    kind: str
    signal: str
    user_id: str
    machine_id: str
    machine: str
    remote_user: str
    session_id: str
    ip_address: str
    flagged_at: str


class AccessLink(TypedDict, total=False):
    relation: str
    ref: str
    holds: bool
    detail: str
    via: str
    reason: str
    granted_by: str
    at: str
    expires_at: str


class AccessExplanation(TypedDict, total=False):
    allowed: bool
    summary: str
    user_id: str
    username: str
    machine_id: str
    machine: str
    access_type: str
    remote_user: str
    chain: list[AccessLink]


class APIKey(TypedDict, total=False):
    id: str
    user_id: str
    name: str
    key_prefix: str
    last_used_at: str
    expires_at: str
    created_at: str
    revoked_at: str


class CreateAPIKeyResponse(TypedDict, total=False):
    id: str
    name: str
    api_key: str
    key_prefix: str
    expires_at: str
    created_at: str


class Permission(TypedDict, total=False):
    id: str
    user_id: str
    machine_id: str
    access_type: str
    remote_users: list[str]
    granted_by: str
    granted_at: str
    expires_at: str


class EffectiveAccess(TypedDict, total=False):
    user_id: str
    username: str
    machine_id: str
    machine_name: str
    access_type: str
    remote_users: list[str]
    source: str
    expires_at: str


class BulkFailure(TypedDict, total=False):
    user_id: str
    machine_id: str
    permission_id: str
    error: str


class BulkResult(TypedDict, total=False):
    granted: int
    revoked: int
    failed: list[BulkFailure]


class Capability(TypedDict, total=False):
    id: str
    machine_id: str
    machine_name: str
    hostname: str
    port: int
    machine_active: bool
    tags: dict[str, str]
    remote_user: str
    access_type: str
    status: str
    source: str
    reason: str
    permission_id: str
    request_id: str
    expires_at: str
    last_used_at: str


class CapabilityCatalog(TypedDict, total=False):
    user_id: str
    scope: str
    generated_at: str
    truncated: bool
    capabilities: list[Capability]


class SetupSteps(TypedDict, total=False):
    admin_exists: bool
    has_machines: bool
    has_connected_agents: bool
    has_users: bool
    has_permissions: bool


class SetupCounts(TypedDict, total=False):
    admins: int
    users: int
    machines: int
    connected_agents: int
    permissions: int


class SetupStatus(TypedDict, total=False):
    complete: bool
    steps: SetupSteps
    counts: SetupCounts
    next: str


class AccessVolume(TypedDict, total=False):
    sessions_total: int
    sessions_active: int
    requests_total: int
    requests_pending: int
    requests_approved: int
    requests_rejected: int


class ApprovalLatency(TypedDict, total=False):
    sample_size: int
    average_seconds: float
    p50_seconds: float
    p95_seconds: float


class TopTarget(TypedDict, total=False):
    machine_id: str
    machine_name: str
    session_count: int


# "from" is a Python keyword, so this one uses the functional form.
UsageDashboard = TypedDict(
    "UsageDashboard",
    {
        "window_hours": int,
        "from": str,
        "to": str,
        "generated_at": str,
        "access_volume": AccessVolume,
        "approval_latency": ApprovalLatency,
        "top_targets": list[TopTarget],
    },
    total=False,
)


class GatewayInfo(TypedDict, total=False):
    public_url: str
    ui_url: str
    ssh_host: str
    ssh_port: int
    api_port: int
    public_ssh_host: str
    public_ssh_port: int


class VersionInfo(TypedDict, total=False):
    version: str
    commit: str
    date: str
    display: str


class TrustedCA(TypedDict, total=False):
    enabled: bool
    user_ca: str
    host_ca: str


class IssuedCert(TypedDict, total=False):
    certificate: str
    serial: str
    expires_at: str


class SSHCertificate(TypedDict, total=False):
    id: str
    serial: str
    cert_type: str
    subject_id: str
    key_id: str
    principals: list[str]
    public_key_fingerprint: str
    issued_at: str
    expires_at: str
    revoked_at: str
    revoked_by: str
    revoke_reason: str


class SSHKey(TypedDict, total=False):
    id: str
    user_id: str
    name: str
    public_key: str
    key_type: str
    created_at: str


class BootstrapCode(TypedDict, total=False):
    code: str
    expires_at: str


class PluginConfigField(TypedDict, total=False):
    key: str
    label: str
    type: str
    secret: bool
    required: bool
    placeholder: str
    help: str
    fields: list[PluginConfigField]


class PluginInfo(TypedDict, total=False):
    name: str
    version: str
    enabled: bool
    configured: bool
    last_error: str
    config: dict[str, Any]
    has_webhook: bool
    schema: list[PluginConfigField]


class PluginConfigResult(TypedDict, total=False):
    plugin: PluginInfo
    configure_error: str


class AgentInstallScriptResponse(TypedDict, total=False):
    script: str
    machine_id: str
    user_id: str
    agent_name: str
    public_key: str
    filename: str
    message: str
    unsigned_build: bool


class RegisterAgentResponse(TypedDict, total=False):
    user_id: str
    machine_id: str
    message: str
    host_certificate: str
    host_ca_public_key: str


class RegisterClientResponse(TypedDict, total=False):
    user_id: str
    message: str


class MFAEnrollResponse(TypedDict, total=False):
    secret: str
    otpauth_url: str
    backup_codes: list[str]
    message: str


class MFAStatus(TypedDict, total=False):
    mfa_enabled: bool
    mfa_required: bool


class WebAuthnCredential(TypedDict, total=False):
    id: str
    name: str
    created_at: str
    cred_id: str


class FileEntry(TypedDict, total=False):
    name: str
    path: str
    is_dir: bool
    size: int
    mtime: int


class FileListResponse(TypedDict, total=False):
    path: str
    entries: list[FileEntry]
    raw: str


class FileUploadResponse(TypedDict, total=False):
    message: str
    path: str
    size: int


class KubeBinding(TypedDict, total=False):
    name: str
    namespace: str
    cluster_role: str
    subject: str
    user: str
    permission_id: str
    expires_at: str


class KubePlan(TypedDict, total=False):
    create: list[KubeBinding]
    update: list[KubeBinding]
    delete: list[KubeBinding]


class KubeReconcileResult(TypedDict, total=False):
    machine_id: str
    dry_run: bool
    in_sync: bool
    plan: KubePlan


# Request bodies. Keys left out are not sent, which is how the update
# endpoints tell "unchanged" from "cleared".


class CreateAPIKeyRequest(TypedDict, total=False):
    name: str
    # Lifetime in DAYS, not seconds. Leave it out for a key that never expires.
    expires_in: int


class CreateAccessRequestRequest(TypedDict, total=False):
    machine_id: str
    reason: str
    # Seconds.
    duration: int
    access_type: str
    remote_users: list[str]


class CreateUserRequest(TypedDict, total=False):
    username: str
    email: str
    public_key: str
    role: str
    is_admin: bool


class UpdateUserRequest(TypedDict, total=False):
    email: str
    public_key: str
    is_admin: bool
    role: str


class CreateMachineRequest(TypedDict, total=False):
    name: str
    hostname: str
    port: int
    tags: dict[str, str]
    agent_id: str
    is_active: bool


class UpdateMachineRequest(TypedDict, total=False):
    name: str
    hostname: str
    port: int
    tags: dict[str, str]
    agent_id: str
    is_active: bool


class GrantPermissionRequest(TypedDict, total=False):
    user_id: str
    machine_id: str
    access_type: str
    remote_users: list[str]
    expires_at: str
    duration_seconds: int


class UpdatePermissionRequest(TypedDict, total=False):
    access_type: str
    remote_users: list[str]
    expires_at: str
    duration_seconds: int


class BulkGrantRequest(TypedDict, total=False):
    user_ids: list[str]
    machine_ids: list[str]
    access_type: str
    remote_users: list[str]
    expires_at: str
    duration_seconds: int


class AgentInstallScriptRequest(TypedDict, total=False):
    name: str
    hostname: str
    port: int
    tags: dict[str, str]
    os: str
    gateway_host: str
    gateway_port: int
    package_base_url: str
    version: str
    # Accept packages the release key did not sign (local snapshot builds).
    # The server audits it.
    allow_unsigned_build: bool


class RegisterAgentRequest(TypedDict, total=False):
    name: str
    hostname: str
    port: int
    public_key: str
    tags: dict[str, str]


class RegisterClientRequest(TypedDict, total=False):
    username: str
    email: str
    public_key: str
    is_admin: bool
