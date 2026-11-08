from __future__ import annotations

import threading
from collections.abc import Iterator
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from orion_belt_sdk import OrionBeltClient


@dataclass
class Recorded:
    method: str
    target: str  # path plus query, as sent
    headers: dict[str, str]  # names lowercased; HTTP header names are case-insensitive
    body: bytes


@dataclass
class RecordingServer:
    """Answers every request with one canned reply and keeps what it received."""

    url: str
    status: int = 200
    body: bytes = b"{}"
    content_type: str = "application/json"
    requests: list[Recorded] = field(default_factory=list)
    # Per-path replies, for flows that make more than one call.
    routes: dict[str, bytes] = field(default_factory=dict)

    def reply(self, status: int, body: str | bytes, content_type: str = "application/json") -> None:
        self.status = status
        self.body = body.encode() if isinstance(body, str) else body
        self.content_type = content_type

    @property
    def last(self) -> Recorded:
        return self.requests[-1]


@pytest.fixture
def server() -> Iterator[RecordingServer]:
    state = RecordingServer(url="")

    class Handler(BaseHTTPRequestHandler):
        def _handle(self) -> None:
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length) if length else b""
            state.requests.append(Recorded(self.command, self.path, {k.lower(): v for k, v in self.headers.items()}, body))
            reply = state.routes.get(self.path.split("?", 1)[0], state.body)
            self.send_response(state.status)
            self.send_header("Content-Type", state.content_type)
            self.send_header("Content-Length", str(len(reply)))
            self.end_headers()
            self.wfile.write(reply)

        do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _handle

        def log_message(self, format: str, *args: object) -> None:
            pass

    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    state.url = f"http://127.0.0.1:{httpd.server_address[1]}"
    thread = threading.Thread(target=httpd.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
    thread.start()
    try:
        yield state
    finally:
        httpd.shutdown()
        httpd.server_close()
        thread.join()


@pytest.fixture
def client(server: RecordingServer) -> OrionBeltClient:
    return OrionBeltClient(server.url, api_key="test-key")
