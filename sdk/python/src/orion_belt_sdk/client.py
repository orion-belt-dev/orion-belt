"""Synchronous client for the Orion Belt HTTP API.

The Go SDK in pkg/sdk is the reference for auth and request behaviour; this
client sends the same requests. It uses only the standard library so it can be
dropped into automation hosts without pulling in an HTTP stack.
"""

from __future__ import annotations

import base64
import json
import mimetypes
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections.abc import Mapping
from typing import Any, Protocol, cast

from .types import (
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
)

DEFAULT_TIMEOUT_SECONDS = 30.0

QueryValue = str | int | bool | None


class OrionBeltError(Exception):
    """A non-2xx response. ``message`` is the server's ``error`` field when it sent one."""

    def __init__(self, status_code: int, message: str, body: str) -> None:
        super().__init__(status_code, message, body)
        self.status_code = status_code
        self.message = message
        self.body = body

    def __str__(self) -> str:
        detail = self.message or self.body
        return f"api error ({self.status_code}): {detail}" if detail else f"api error ({self.status_code})"


class Signer(Protocol):
    """Proves possession of an SSH key for the challenge logins.

    ``public_key`` is the authorized_keys line. ``sign`` returns the SSH
    signature format (``ssh-ed25519``, ``rsa-sha2-256``, ...) and the raw
    signature blob over ``data``.
    """

    @property
    def public_key(self) -> str: ...

    def sign(self, data: bytes) -> tuple[str, bytes]: ...


def _seg(value: str) -> str:
    """Escapes one path segment so an ID can never add or climb path levels."""
    return urllib.parse.quote(value, safe="")


def _query(params: Mapping[str, QueryValue]) -> str:
    """Encodes the set parameters; None, "" and 0 mean "use the server default"."""
    kept: dict[str, str] = {}
    for key, value in params.items():
        if value is None or value == "" or value is False:
            continue
        if isinstance(value, bool):
            kept[key] = "true"
        elif isinstance(value, int):
            if value <= 0:
                continue
            kept[key] = str(value)
        else:
            kept[key] = value
    encoded = urllib.parse.urlencode(kept)
    return f"?{encoded}" if encoded else ""


def _error_message(body: str) -> str:
    try:
        parsed = json.loads(body)
    except json.JSONDecodeError:
        return body.strip()
    if isinstance(parsed, dict):
        for key in ("error", "message"):
            value = parsed.get(key)
            if isinstance(value, str) and value:
                return value
    return body.strip()


class OrionBeltClient:
    """Client for one Orion Belt server.

    Auth precedence matches the server: ``X-API-Key`` wins over
    ``X-Session-Token``; a bearer token is sent alongside either. The login
    helpers store the credential they obtain on the client.
    """

    def __init__(
        self,
        base_url: str,
        *,
        api_key: str | None = None,
        session_token: str | None = None,
        bearer_token: str | None = None,
        timeout_seconds: float = DEFAULT_TIMEOUT_SECONDS,
        user_agent: str | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> None:
        base_url = base_url.strip().rstrip("/")
        if not base_url:
            raise ValueError("base_url is required")
        self.base_url = base_url
        self.timeout_seconds = timeout_seconds
        self.headers = dict(headers or {})
        if user_agent and user_agent.strip():
            # The gateway records the User-Agent as the device signal on access requests.
            self.headers["User-Agent"] = user_agent.strip()
        self.api_key: str | None = None
        self.session_token: str | None = None
        self.bearer_token: str | None = None
        self.set_api_key(api_key)
        self.set_session_token(session_token)
        self.set_bearer_token(bearer_token)

    def set_api_key(self, api_key: str | None) -> None:
        self.api_key = api_key.strip() if api_key and api_key.strip() else None

    def set_session_token(self, session_token: str | None) -> None:
        self.session_token = session_token.strip() if session_token and session_token.strip() else None

    def set_bearer_token(self, bearer_token: str | None) -> None:
        self.bearer_token = bearer_token.strip() if bearer_token and bearer_token.strip() else None

    # Transport

    def url(self, path: str) -> str:
        """Absolute URL for an API path; paths without /api/ are taken as /api/v1-relative."""
        if path.startswith(("http://", "https://")):
            return path
        if not path.startswith("/"):
            path = "/" + path
        if not path.startswith("/api/"):
            path = "/api/v1" + path
        return self.base_url + path

    def auth_headers(self) -> dict[str, str]:
        """The credential headers this client sends, e.g. for opening the WebSocket endpoints."""
        headers: dict[str, str] = {}
        if self.api_key:
            headers["X-API-Key"] = self.api_key
        elif self.session_token:
            headers["X-Session-Token"] = self.session_token
        if self.bearer_token:
            headers["Authorization"] = f"Bearer {self.bearer_token}"
        return headers

    def _send(self, method: str, path: str, data: bytes | None, content_type: str | None, auth: bool) -> bytes:
        headers = dict(self.headers)
        if content_type:
            headers["Content-Type"] = content_type
        if auth:
            headers.update(self.auth_headers())
        request = urllib.request.Request(self.url(path), data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                return cast(bytes, response.read())
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            raise OrionBeltError(exc.code, _error_message(body), body.strip()) from exc

    def request(self, method: str, path: str, body: Any = None, *, auth: bool = True) -> Any:
        """Sends a JSON request and returns the decoded response (None for an empty body).

        Use it for endpoints this client has no method for yet.
        """
        data = None if body is None else json.dumps(body).encode("utf-8")
        payload = self._send(method, path, data, "application/json" if data is not None else None, auth)
        if not payload:
            return None
        return json.loads(payload.decode("utf-8"))

    def request_bytes(self, method: str, path: str, *, auth: bool = True) -> bytes:
        """Sends a request without a body and returns the raw response."""
        return self._send(method, path, None, None, auth)

    def _multipart(self, path: str, fields: Mapping[str, str], file_name: str, content: bytes) -> Any:
        boundary = f"orionbelt{uuid.uuid4().hex}"
        parts: list[bytes] = []
        for name, value in fields.items():
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
        # Quotes and line breaks in a filename would end the header early.
        safe_name = file_name.replace("\\", "\\\\").replace('"', '\\"').replace("\r", "").replace("\n", "")
        mime = mimetypes.guess_type(file_name)[0] or "application/octet-stream"
        parts.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{safe_name}"\r\n'
            f"Content-Type: {mime}\r\n\r\n".encode()
        )
        parts.append(content)
        parts.append(f"\r\n--{boundary}--\r\n".encode())
        payload = self._send("POST", path, b"".join(parts), f"multipart/form-data; boundary={boundary}", True)
        return json.loads(payload.decode("utf-8")) if payload else None

    def _list(self, path: str, key: str | None = None) -> list[Any]:
        result = self.request("GET", path)
        if key is not None:
            result = result.get(key) if isinstance(result, dict) else None
        return list(result or [])

    # Public: registration and login

    def get_gateway_info(self) -> GatewayInfo:
        """Addresses the gateway advertises to clients and agents. Needs no credentials."""
        return cast(GatewayInfo, self.request("GET", "/gateway-info", auth=False))

    def get_version(self) -> VersionInfo:
        """The server's build version. Needs no credentials."""
        return cast(VersionInfo, self.request("GET", "/version", auth=False))

    def get_openapi_spec(self) -> str:
        """The server's OpenAPI 3 document, as YAML text."""
        return self.request_bytes("GET", "/openapi.yaml", auth=False).decode("utf-8")

    def get_openapi_index(self) -> dict[str, Any]:
        """Where to find the OpenAPI document (/openapi.json answers with pointers, not the spec)."""
        return cast(dict[str, Any], self.request("GET", "/openapi.json", auth=False))

    def register_agent(self, req: RegisterAgentRequest) -> RegisterAgentResponse:
        """Registers an agent. The server requires an admin or operator, so credentials are sent."""
        return cast(RegisterAgentResponse, self.request("POST", "/public/register/agent", req))

    def register_client(self, req: RegisterClientRequest) -> RegisterClientResponse:
        """Registers a user. ``is_admin`` is honoured only for an admin caller or the first account."""
        return cast(RegisterClientResponse, self.request("POST", "/public/register/client", req))

    def issue_challenge(self, username: str) -> str:
        """A one-time challenge for the SSH-key logins."""
        result = self.request("POST", "/public/auth/challenge", {"username": username}, auth=False) or {}
        challenge = result.get("challenge")
        if not challenge:
            raise ValueError("no challenge returned")
        return cast(str, challenge)

    def _signed_login(self, path: str, username: str, signer: Signer, totp_code: str | None) -> dict[str, Any]:
        challenge = self.issue_challenge(username)
        fmt, blob = signer.sign(challenge.encode("utf-8"))
        body: dict[str, Any] = {
            "username": username,
            "public_key": signer.public_key,
            "challenge": challenge,
            "signature_format": fmt,
            "signature": base64.b64encode(blob).decode("ascii"),
        }
        if totp_code and totp_code.strip():
            body["totp_code"] = totp_code.strip()
        return cast(dict[str, Any], self.request("POST", path, body, auth=False) or {})

    def _store_session(self, result: Mapping[str, Any]) -> None:
        if result.get("session_token"):
            self.session_token = result["session_token"]
        if result.get("access_token"):
            self.bearer_token = result["access_token"]

    def login_with_ssh_session(self, username: str, signer: Signer, totp_code: str | None = None) -> PasswordLoginResult:
        """SSH challenge login that returns a session token (POST /public/login)."""
        result = self._signed_login("/public/login", username, signer, totp_code)
        if not result.get("session_token"):
            raise ValueError("no session token returned")
        self._store_session(result)
        return cast(PasswordLoginResult, result)

    def login_with_ssh_key(self, username: str, signer: Signer, totp_code: str | None = None) -> APIKeyLoginResult:
        """SSH challenge login that returns an API key (POST /public/login/key)."""
        result = self._signed_login("/public/login/key", username, signer, totp_code)
        if not result.get("api_key"):
            raise ValueError("no API key returned")
        self.api_key = result["api_key"]
        return cast(APIKeyLoginResult, result)

    def login_with_ssh_jwt(self, username: str, signer: Signer, totp_code: str | None = None) -> JWTLoginResult:
        """SSH challenge login that returns a JWT bearer token (POST /public/login/token)."""
        result = self._signed_login("/public/login/token", username, signer, totp_code)
        if not result.get("access_token"):
            raise ValueError("no access token returned")
        self.bearer_token = result["access_token"]
        return cast(JWTLoginResult, result)

    def login_with_password(self, username: str, password: str, totp_code: str = "") -> PasswordLoginResult:
        body = {"username": username, "password": password, "totp_code": totp_code}
        result = self.request("POST", "/public/login/password", body, auth=False) or {}
        if not result.get("session_token"):
            raise ValueError("no session token returned")
        self._store_session(result)
        return cast(PasswordLoginResult, result)

    def redeem_browser_bootstrap(self, code: str) -> PasswordLoginResult:
        result = self.request("POST", "/public/auth/browser-bootstrap/redeem", {"code": code}, auth=False) or {}
        self._store_session(result)
        return cast(PasswordLoginResult, result)

    def webauthn_login_begin(self, username: str) -> dict[str, Any]:
        return cast(dict[str, Any], self.request("POST", "/public/webauthn/login/begin", {"username": username}, auth=False))

    def webauthn_login_finish(self, username: str, response: Mapping[str, Any]) -> PasswordLoginResult:
        body = {"username": username, "response": dict(response)}
        result = self.request("POST", "/public/webauthn/login/finish", body, auth=False) or {}
        self._store_session(result)
        return cast(PasswordLoginResult, result)

    def plugin_webhook(self, plugin: str, path: str, method: str = "POST", body: Any = None) -> Any:
        """Calls a plugin's inbound webhook. The plugin authenticates the caller itself."""
        sub = "/".join(_seg(part) for part in path.lstrip("/").split("/"))
        return self.request(method, f"/public/plugins/{_seg(plugin)}/{sub}", body, auth=False)

    # Current user

    def logout(self) -> None:
        """Ends the HTTP session and drops the session and bearer tokens from this client.

        The server does not revoke a JWT at logout: one issued alongside the
        session stays valid until it expires.
        """
        self.request("POST", "/logout")
        self.session_token = None
        self.bearer_token = None

    def get_current_user(self) -> AuthUser:
        return cast(AuthUser, self.request("GET", "/auth/me"))

    def request_browser_bootstrap(self) -> BootstrapCode:
        """A one-time code that opens a browser session as this user."""
        return cast(BootstrapCode, self.request("POST", "/auth/browser-bootstrap"))

    def set_password(self, password: str, totp_code: str) -> None:
        self.request("POST", "/auth/password", {"password": password, "totp_code": totp_code})

    def clear_password(self, totp_code: str) -> None:
        self.request("DELETE", "/auth/password", {"totp_code": totp_code})

    def mfa_enroll(self) -> MFAEnrollResponse:
        return cast(MFAEnrollResponse, self.request("POST", "/mfa/enroll", {}))

    def mfa_confirm(self, code: str) -> None:
        self.request("POST", "/mfa/confirm", {"code": code})

    def mfa_disable(self, code: str) -> None:
        self.request("POST", "/mfa/disable", {"code": code})

    def mfa_status(self) -> MFAStatus:
        return cast(MFAStatus, self.request("GET", "/mfa/status"))

    def webauthn_register_begin(self) -> dict[str, Any]:
        return cast(dict[str, Any], self.request("POST", "/webauthn/register/begin", {}))

    def webauthn_register_finish(self, response: Mapping[str, Any]) -> str:
        """Completes registration and returns the new credential's ID."""
        result = self.request("POST", "/webauthn/register/finish", dict(response)) or {}
        return cast(str, result.get("id", ""))

    def webauthn_credentials(self) -> list[WebAuthnCredential]:
        return self._list("/webauthn/credentials")

    def webauthn_delete_credential(self, credential_id: str) -> None:
        self.request("DELETE", f"/webauthn/credentials/{_seg(credential_id)}")

    def list_ssh_keys(self) -> list[SSHKey]:
        return self._list("/ssh-keys")

    def add_ssh_key(self, name: str, public_key: str) -> SSHKey:
        return cast(SSHKey, self.request("POST", "/ssh-keys", {"name": name, "public_key": public_key}))

    def delete_ssh_key(self, key_id: str) -> None:
        self.request("DELETE", f"/ssh-keys/{_seg(key_id)}")

    def create_api_key(self, req: CreateAPIKeyRequest) -> CreateAPIKeyResponse:
        """Creates a key; the raw key is in ``api_key`` and is returned only this once."""
        return cast(CreateAPIKeyResponse, self.request("POST", "/api-keys", req))

    def list_api_keys(self) -> list[APIKey]:
        return self._list("/api-keys", "api_keys")

    def revoke_api_key(self, key_id: str) -> None:
        self.request("POST", f"/api-keys/{_seg(key_id)}/revoke")

    def delete_api_key(self, key_id: str) -> None:
        self.request("DELETE", f"/api-keys/{_seg(key_id)}")

    # Notifications

    def list_notifications(self, limit: int = 0) -> list[Notification]:
        return self._list("/notifications" + _query({"limit": limit}))

    def unread_notification_count(self) -> int:
        result = self.request("GET", "/notifications/unread-count") or {}
        return int(result.get("unread", 0))

    def mark_notification_read(self, notification_id: str) -> None:
        self.request("POST", f"/notifications/{_seg(notification_id)}/read")

    def mark_all_notifications_read(self) -> None:
        self.request("POST", "/notifications/read-all")

    def get_notification_prefs(self) -> NotificationPrefsResult:
        """The caller's preferences and the admin bounds they were resolved within."""
        return cast(NotificationPrefsResult, self.request("GET", "/notifications/prefs"))

    def put_notification_prefs(self, prefs: NotificationPrefs) -> NotificationPrefsResult:
        """Stores preferences. Values outside the admin policy are clamped; ``adjusted`` says so."""
        return cast(NotificationPrefsResult, self.request("PUT", "/notifications/prefs", prefs))

    # Users, machines, permissions

    def list_users(self) -> list[User]:
        return self._list("/users")

    def get_user(self, user_id: str) -> User:
        return cast(User, self.request("GET", f"/users/{_seg(user_id)}"))

    def create_user(self, req: CreateUserRequest) -> User:
        return cast(User, self.request("POST", "/admin/users", req))

    def update_user(self, user_id: str, req: UpdateUserRequest) -> User:
        return cast(User, self.request("PUT", f"/admin/users/{_seg(user_id)}", req))

    def delete_user(self, user_id: str) -> None:
        self.request("DELETE", f"/admin/users/{_seg(user_id)}")

    def list_machines(self) -> list[Machine]:
        return self._list("/machines")

    def get_machine(self, machine_id: str) -> Machine:
        return cast(Machine, self.request("GET", f"/machines/{_seg(machine_id)}"))

    def get_machine_by_name(self, name: str) -> Machine:
        """Resolves a machine by name from list_machines; raises LookupError when absent."""
        for machine in self.list_machines():
            if machine.get("name") == name:
                return machine
        raise LookupError(f"machine {name!r} not found")

    def create_machine(self, req: CreateMachineRequest) -> Machine:
        return cast(Machine, self.request("POST", "/admin/machines", req))

    def update_machine(self, machine_id: str, req: UpdateMachineRequest) -> Machine:
        return cast(Machine, self.request("PUT", f"/admin/machines/{_seg(machine_id)}", req))

    def delete_machine(self, machine_id: str, archive: bool = False) -> None:
        """Deletes a machine, or with ``archive`` deactivates it and keeps its history."""
        self.request("DELETE", f"/admin/machines/{_seg(machine_id)}" + _query({"archive": archive}))

    def kube_reconcile(self, machine_id: str, dry_run: bool = False) -> KubeReconcileResult:
        """Reconciles a cluster's RoleBindings with its grants; ``dry_run`` only reports drift."""
        method = "GET" if dry_run else "POST"
        return cast(KubeReconcileResult, self.request(method, f"/admin/machines/{_seg(machine_id)}/kubernetes/reconcile"))

    def get_user_permissions(self, user_id: str) -> list[Permission]:
        return self._list(f"/permissions/user/{_seg(user_id)}")

    def get_machine_permissions(self, machine_id: str) -> list[Permission]:
        return self._list(f"/permissions/machine/{_seg(machine_id)}")

    def list_all_permissions(self, limit: int = 0) -> list[Permission]:
        return self._list("/admin/permissions" + _query({"limit": limit}))

    def grant_permission(self, req: GrantPermissionRequest) -> Permission:
        return cast(Permission, self.request("POST", "/admin/permissions", req))

    def update_permission(self, permission_id: str, req: UpdatePermissionRequest) -> Permission:
        return cast(Permission, self.request("PATCH", f"/admin/permissions/{_seg(permission_id)}", req))

    def revoke_permission(self, permission_id: str) -> None:
        self.request("DELETE", f"/admin/permissions/{_seg(permission_id)}")

    def bulk_grant_permissions(self, req: BulkGrantRequest) -> BulkResult:
        """Grants one access type on every user x machine pair."""
        return cast(BulkResult, self.request("POST", "/admin/permissions/bulk", req))

    def bulk_revoke_permissions(self, ids: list[str]) -> BulkResult:
        return cast(BulkResult, self.request("POST", "/admin/permissions/bulk-revoke", {"ids": ids}))

    def effective_permissions(self, *, user_id: str = "", machine_id: str = "") -> list[EffectiveAccess]:
        """Resolved access for one user or one machine; set exactly one."""
        return self._list("/admin/permissions/effective" + _query({"user_id": user_id, "machine_id": machine_id}))

    def list_capabilities(self, scope: str = "", user_id: str = "") -> CapabilityCatalog:
        """What the caller holds or could request. ``scope`` is "related" (default) or "all"."""
        return cast(CapabilityCatalog, self.request("GET", "/capabilities" + _query({"scope": scope, "user_id": user_id})))

    def explain_access(self, machine: str, remote_user: str, access_type: str = "", user: str = "") -> AccessExplanation:
        """Whether a user (default: the caller) may reach a machine as ``remote_user``, and why."""
        query = _query({"machine": machine, "remote_user": remote_user, "access_type": access_type, "user": user})
        return cast(AccessExplanation, self.request("GET", "/access/explain" + query))

    # Access requests

    def list_access_requests(self, status: str = "") -> list[AccessRequest]:
        return self._list("/access-requests" + _query({"status": status}))

    def create_access_request(self, req: CreateAccessRequestRequest) -> AccessRequest:
        return cast(AccessRequest, self.request("POST", "/access-requests", req))

    def get_access_request(self, request_id: str) -> AccessRequest:
        return cast(AccessRequest, self.request("GET", f"/access-requests/{_seg(request_id)}"))

    def list_pending_access_requests(self) -> list[AccessRequest]:
        return self._list("/admin/access-requests/pending")

    def approve_access_request(self, request_id: str, reviewer_id: str = "") -> None:
        self.request("POST", f"/admin/access-requests/{_seg(request_id)}/approve", {"reviewer_id": reviewer_id})

    def reject_access_request(self, request_id: str, reviewer_id: str = "") -> None:
        self.request("POST", f"/admin/access-requests/{_seg(request_id)}/reject", {"reviewer_id": reviewer_id})

    def get_risk_policy(self) -> RiskPolicy:
        return cast(RiskPolicy, self.request("GET", "/admin/risk-policy"))

    def put_risk_policy(self, policy: RiskPolicy) -> RiskPolicy:
        """Replaces the risk policy outright; send the whole document."""
        return cast(RiskPolicy, self.request("PUT", "/admin/risk-policy", policy))

    # Sessions, audit, reporting

    def list_sessions(self, status: str = "") -> list[Session]:
        return self._list("/sessions" + _query({"status": status}))

    def list_active_sessions(self) -> list[Session]:
        return self._list("/sessions/active")

    def get_session(self, session_id: str) -> Session:
        return cast(Session, self.request("GET", f"/sessions/{_seg(session_id)}"))

    def get_session_content(self, session_id: str) -> bytes:
        """The raw session recording."""
        return self.request_bytes("GET", f"/sessions/{_seg(session_id)}/content")

    def list_audit_logs(self, limit: int = 0) -> list[AuditLog]:
        return self._list("/audit-logs" + _query({"limit": limit}))

    def export_report(self, report_name: str, format: str = "", params: Mapping[str, str] | None = None) -> bytes:
        """Report bytes. ``params`` carries report options such as from/to or as_of."""
        query: dict[str, QueryValue] = dict(params or {})
        query["format"] = format
        return self.request_bytes("GET", f"/reports/{_seg(report_name)}/export" + _query(query))

    def get_usage_dashboard(self, window_hours: int = 0, top: int = 0) -> UsageDashboard:
        return cast(UsageDashboard, self.request("GET", "/dashboard/usage" + _query({"window_hours": window_hours, "top": top})))

    def list_anomalies(self, limit: int = 0, user_id: str = "") -> list[AccessAnomaly]:
        return self._list("/anomalies" + _query({"limit": limit, "user_id": user_id}))

    def get_setup_status(self) -> SetupStatus:
        return cast(SetupStatus, self.request("GET", "/setup/status"))

    # Notification policy and templates (admin)

    def get_notification_policy(self) -> NotificationPolicyResult:
        return cast(NotificationPolicyResult, self.request("GET", "/admin/notifications/policy"))

    def put_notification_policy(self, policy: NotificationPolicy) -> NotificationPolicy:
        """Replaces the policy outright; send the whole document."""
        return cast(NotificationPolicy, self.request("PUT", "/admin/notifications/policy", policy))

    def list_notification_templates(self) -> list[NotificationTemplateEntry]:
        return self._list("/admin/notifications/templates", "templates")

    def put_notification_template(self, event: str, template: NotificationTemplate) -> NotificationTemplate:
        # The server rejects a body naming a different event than the path.
        body = {**template, "event_type": event}
        return cast(NotificationTemplate, self.request("PUT", f"/admin/notifications/templates/{_seg(event)}", body))

    def delete_notification_template(self, event: str) -> None:
        """Drops the override, returning the event to its built-in copy."""
        self.request("DELETE", f"/admin/notifications/templates/{_seg(event)}")

    # Plugins and agents (admin)

    def list_plugins(self) -> list[PluginInfo]:
        return self._list("/admin/plugins", "plugins")

    def update_plugin_config(self, name: str, enabled: bool, config: Mapping[str, Any]) -> PluginConfigResult:
        """Stores config. ``configure_error`` reports a config the plugin rejected after saving."""
        body = {"enabled": enabled, "config": dict(config)}
        return cast(PluginConfigResult, self.request("PUT", f"/admin/plugins/{_seg(name)}/config", body))

    def enable_plugin(self, name: str) -> PluginInfo:
        result = self.request("POST", f"/admin/plugins/{_seg(name)}/enable") or {}
        return cast(PluginInfo, result.get("plugin", {}))

    def disable_plugin(self, name: str) -> PluginInfo:
        result = self.request("POST", f"/admin/plugins/{_seg(name)}/disable") or {}
        return cast(PluginInfo, result.get("plugin", {}))

    def list_connected_agents(self) -> list[str]:
        """Machine IDs with a live agent connection."""
        return self._list("/admin/agents/connected", "agents")

    def send_agent_command(self, machine_id: str, command: str) -> str:
        """Sends a control command (reload, drain, undrain, update) and returns its output."""
        result = self.request("POST", f"/admin/agents/{_seg(machine_id)}/command", {"command": command}) or {}
        return cast(str, result.get("output", ""))

    def disconnect_agent(self, machine_id: str) -> None:
        self.request("POST", f"/admin/agents/{_seg(machine_id)}/disconnect")

    def generate_agent_install_script(self, req: AgentInstallScriptRequest) -> AgentInstallScriptResponse:
        return cast(AgentInstallScriptResponse, self.request("POST", "/admin/agents/install-script", req))

    # SSH certificate authority

    def get_trusted_ca(self) -> TrustedCA:
        return cast(TrustedCA, self.request("GET", "/ssh-cert/ca"))

    def issue_user_cert(self, public_key: str, ttl_hours: int = 0) -> IssuedCert:
        return cast(IssuedCert, self.request("POST", "/ssh-cert", {"public_key": public_key, "ttl_hours": ttl_hours}))

    def export_ca(self) -> TrustedCA:
        return cast(TrustedCA, self.request("GET", "/admin/ca/export"))

    def list_ssh_certificates(self, cert_type: str = "") -> list[SSHCertificate]:
        return self._list("/admin/ssh-certificates" + _query({"cert_type": cert_type}), "certificates")

    def revoke_ssh_certificate(self, serial: str, reason: str = "") -> None:
        self.request("POST", f"/admin/ssh-certificates/{_seg(serial)}/revoke", {"reason": reason})

    # Remote files

    def list_files(self, machine: str, path: str = "", remote_user: str = "") -> FileListResponse:
        """Lists a directory; the server defaults to "/" as root."""
        query = _query({"machine": machine, "path": path, "user": remote_user})
        result = cast(FileListResponse, self.request("GET", "/files/list" + query))
        result.setdefault("entries", [])
        return result

    def download_file(self, machine: str, path: str, remote_user: str = "") -> bytes:
        return self.request_bytes("GET", "/files/download" + _query({"machine": machine, "path": path, "user": remote_user}))

    def upload_file(self, machine: str, path: str, file_name: str, content: bytes, remote_user: str = "") -> FileUploadResponse:
        fields = {"machine": machine, "path": path}
        if remote_user:
            fields["user"] = remote_user
        return cast(FileUploadResponse, self._multipart("/files/upload", fields, file_name, content))

    def make_dir(self, machine: str, path: str, remote_user: str = "") -> None:
        body = {"machine": machine, "path": path}
        if remote_user:
            body["user"] = remote_user
        self.request("POST", "/files/mkdir", body)

    def delete_file(self, machine: str, path: str, remote_user: str = "") -> None:
        self.request("DELETE", "/files" + _query({"machine": machine, "path": path, "user": remote_user}))

    # WebSocket endpoints. These return the ws:// or wss:// URL; open it with
    # any WebSocket library, sending auth_headers() on the upgrade request.

    def _ws_url(self, path: str) -> str:
        url = self.url(path)
        if url.startswith("https://"):
            return "wss://" + url[len("https://") :]
        return "ws://" + url[len("http://") :]

    def terminal_ws_url(self, machine: str, remote_user: str = "") -> str:
        """URL of an interactive web terminal on ``machine``."""
        return self._ws_url("/terminal/ws" + _query({"machine": machine, "user": remote_user}))

    def session_watch_ws_url(self, session_id: str) -> str:
        """URL that streams a live session's output to a watcher."""
        return self._ws_url(f"/sessions/{_seg(session_id)}/watch")
