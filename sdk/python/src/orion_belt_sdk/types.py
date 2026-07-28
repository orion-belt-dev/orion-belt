from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class ChallengeResponse:
    challenge: str


@dataclass(slots=True)
class LoginUser:
    id: str
    username: str
    email: str
    is_admin: bool
    role: str | None = None
    mfa_enabled: bool | None = None
    password_set: bool | None = None
    must_set_password: bool | None = None


@dataclass(slots=True)
class LoginResponse:
    session_token: str
    expires_at: str
    access_token: str | None = None
    user: LoginUser | None = None


@dataclass(slots=True)
class LoginWithKeyResponse:
    api_key: str
    expires_at: str | None = None
    user: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class JWTLoginResponse:
    access_token: str
    token_type: str
    expires_at: str
    user: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class AuthMe:
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
    created_at: str | None = None
    updated_at: str | None = None


@dataclass(slots=True)
class Machine:
    id: str
    name: str
    hostname: str
    port: int
    agent_id: str | None = None
    is_active: bool | None = None
    tags: dict[str, str] | None = None


@dataclass(slots=True)
class Session:
    id: str
    user_id: str
    machine_id: str
    remote_user: str
    source: str
    start_time: str
    status: str
    end_time: str | None = None
    recording_path: str | None = None
    created_at: str | None = None


@dataclass(slots=True)
class UsageDashboard:
    window_hours: int
    from_: str
    to: str
    generated_at: str
    access_volume: dict[str, int]
    approval_latency: dict[str, float | int]
    top_targets: list[dict[str, Any]]


@dataclass(slots=True)
class FileEntry:
    name: str
    path: str
    is_dir: bool
    size: int
    mtime: int


@dataclass(slots=True)
class FileListResponse:
    path: str
    entries: list[FileEntry] = field(default_factory=list)
    raw: str | None = None


@dataclass(slots=True)
class FileUploadResponse:
    message: str
    path: str
    size: int