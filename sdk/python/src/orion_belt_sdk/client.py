from __future__ import annotations

from dataclasses import dataclass
from io import BytesIO
import json
import mimetypes
import urllib.error
import urllib.parse
import urllib.request
import uuid
from typing import Any

from .types import (
    AuthMe,
    ChallengeResponse,
    FileEntry,
    FileListResponse,
    FileUploadResponse,
    JWTLoginResponse,
    LoginResponse,
    LoginWithKeyResponse,
    Machine,
    UsageDashboard,
)


@dataclass(slots=True)
class OrionBeltError(Exception):
    status_code: int
    message: str
    body: str

    def __str__(self) -> str:
        return f"api error ({self.status_code}): {self.message or self.body}".strip()


class OrionBeltClient:
    def __init__(
        self,
        base_url: str,
        *,
        api_key: str | None = None,
        session_token: str | None = None,
        bearer_token: str | None = None,
        timeout_seconds: float = 30.0,
        headers: dict[str, str] | None = None,
    ) -> None:
        base_url = base_url.strip().rstrip("/")
        if not base_url:
            raise ValueError("base_url is required")
        self.base_url = base_url
        self.api_key = api_key.strip() if api_key else None
        self.session_token = session_token.strip() if session_token else None
        self.bearer_token = bearer_token.strip() if bearer_token else None
        self.timeout_seconds = timeout_seconds
        self.headers = dict(headers or {})

    def set_api_key(self, api_key: str | None) -> None:
        self.api_key = api_key.strip() if api_key else None

    def set_session_token(self, session_token: str | None) -> None:
        self.session_token = session_token.strip() if session_token else None

    def set_bearer_token(self, bearer_token: str | None) -> None:
        self.bearer_token = bearer_token.strip() if bearer_token else None

    def _full_url(self, path: str) -> str:
        if path.startswith("http://") or path.startswith("https://"):
            return path
        if not path.startswith("/"):
            path = "/" + path
        if not path.startswith("/api/"):
            path = "/api/v1" + path
        return self.base_url + path

    def _headers(self, content_type: str | None = None, auth: bool = True) -> dict[str, str]:
        headers = dict(self.headers)
        if content_type:
            headers["Content-Type"] = content_type
        if auth:
            if self.api_key:
                headers["X-API-Key"] = self.api_key
            elif self.session_token:
                headers["X-Session-Token"] = self.session_token
            if self.bearer_token:
                headers["Authorization"] = f"Bearer {self.bearer_token}"
        return headers

    def _request(self, method: str, path: str, *, body: Any = None, auth: bool = True) -> Any:
        data: bytes | None = None
        headers: dict[str, str]
        if isinstance(body, (bytes, bytearray)):
            data = bytes(body)
            headers = self._headers(auth=auth)
        elif body is None:
            headers = self._headers(auth=auth)
        else:
            data = json.dumps(body).encode("utf-8")
            headers = self._headers("application/json", auth=auth)

        request = urllib.request.Request(self._full_url(path), data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                payload = response.read()
                if not payload:
                    return None
                return json.loads(payload.decode("utf-8"))
        except urllib.error.HTTPError as exc:
            body_text = exc.read().decode("utf-8", errors="replace")
            message = body_text.strip()
            try:
                parsed = json.loads(body_text)
                message = parsed.get("error") or parsed.get("message") or message
            except json.JSONDecodeError:
                pass
            raise OrionBeltError(exc.code, message, body_text) from exc

    def _request_bytes(self, method: str, path: str, *, auth: bool = True) -> bytes:
        request = urllib.request.Request(self._full_url(path), headers=self._headers(auth=auth), method=method)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                return response.read()
        except urllib.error.HTTPError as exc:
            body_text = exc.read().decode("utf-8", errors="replace")
            raise OrionBeltError(exc.code, body_text.strip(), body_text) from exc

    def _multipart(self, fields: dict[str, str], file_field: str, file_name: str, file_bytes: bytes) -> tuple[bytes, str]:
        boundary = f"----orionbelt{uuid.uuid4().hex}"
        buffer = BytesIO()
        for name, value in fields.items():
            buffer.write(f"--{boundary}\r\n".encode())
            buffer.write(f'Content-Disposition: form-data; name="{name}"\r\n\r\n'.encode())
            buffer.write(value.encode())
            buffer.write(b"\r\n")
        mime = mimetypes.guess_type(file_name)[0] or "application/octet-stream"
        buffer.write(f"--{boundary}\r\n".encode())
        buffer.write(f'Content-Disposition: form-data; name="{file_field}"; filename="{file_name}"\r\n'.encode())
        buffer.write(f"Content-Type: {mime}\r\n\r\n".encode())
        buffer.write(file_bytes)
        buffer.write(b"\r\n")
        buffer.write(f"--{boundary}--\r\n".encode())
        return buffer.getvalue(), boundary

    def issue_challenge(self, username: str) -> str:
        result = self._request("POST", "/public/auth/challenge", body={"username": username}, auth=False)
        return ChallengeResponse(**result).challenge

    def login_with_password(self, username: str, password: str, totp_code: str) -> LoginResponse:
        result = LoginResponse(**self._request("POST", "/public/login/password", body={"username": username, "password": password, "totp_code": totp_code}, auth=False))
        self.session_token = result.session_token
        if result.access_token:
            self.bearer_token = result.access_token
        return result

    def login_with_key(self, payload: dict[str, Any]) -> LoginWithKeyResponse:
        result = LoginWithKeyResponse(**self._request("POST", "/public/login/key", body=payload, auth=False))
        self.api_key = result.api_key
        return result

    def login_with_jwt(self, payload: dict[str, Any]) -> JWTLoginResponse:
        result = JWTLoginResponse(**self._request("POST", "/public/login/token", body=payload, auth=False))
        self.bearer_token = result.access_token
        return result

    def get_current_user(self) -> AuthMe:
        return AuthMe(**self._request("GET", "/auth/me"))

    def list_machines(self) -> list[Machine]:
        return [Machine(**item) for item in self._request("GET", "/machines")]

    def get_usage_dashboard(self, window_hours: int = 24, top: int = 10) -> UsageDashboard:
        query = urllib.parse.urlencode({"window_hours": window_hours, "top": top})
        payload = self._request("GET", f"/dashboard/usage?{query}")
        return UsageDashboard(
            window_hours=payload["window_hours"],
            from_=payload["from"],
            to=payload["to"],
            generated_at=payload["generated_at"],
            access_volume=payload["access_volume"],
            approval_latency=payload["approval_latency"],
            top_targets=payload["top_targets"],
        )

    def export_report(self, report_name: str, format: str = "csv") -> bytes:
        query = urllib.parse.urlencode({"format": format})
        return self._request_bytes("GET", f"/reports/{urllib.parse.quote(report_name)}/export?{query}")

    def list_files(self, machine: str, path: str = "/", remote_user: str = "root") -> FileListResponse:
        query = urllib.parse.urlencode({"machine": machine, "path": path, "user": remote_user})
        payload = self._request("GET", f"/files/list?{query}")
        entries = [FileEntry(**item) for item in payload.get("entries", [])]
        return FileListResponse(path=payload["path"], entries=entries, raw=payload.get("raw"))

    def download_file(self, machine: str, path: str, remote_user: str = "root") -> bytes:
        query = urllib.parse.urlencode({"machine": machine, "path": path, "user": remote_user})
        return self._request_bytes("GET", f"/files/download?{query}")

    def upload_file(self, machine: str, path: str, file_name: str, content: bytes, remote_user: str = "root") -> FileUploadResponse:
        body, boundary = self._multipart({"machine": machine, "path": path, "user": remote_user}, "file", file_name, content)
        request = urllib.request.Request(
            self._full_url("/files/upload"),
            data=body,
            headers={**self._headers(auth=True), "Content-Type": f"multipart/form-data; boundary={boundary}"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                payload = json.loads(response.read().decode("utf-8"))
                return FileUploadResponse(**payload)
        except urllib.error.HTTPError as exc:
            body_text = exc.read().decode("utf-8", errors="replace")
            raise OrionBeltError(exc.code, body_text.strip(), body_text) from exc