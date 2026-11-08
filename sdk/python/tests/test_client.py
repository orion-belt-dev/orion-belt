from __future__ import annotations

import base64
import json
import urllib.parse
from collections.abc import Callable

import pytest

from orion_belt_sdk import OrionBeltClient, OrionBeltError, __version__

from .conftest import RecordingServer
from .test_contract import LOGIN_REPLY, FakeSigner


def query_of(target: str) -> dict[str, list[str]]:
    return urllib.parse.parse_qs(urllib.parse.urlsplit(target).query)


def test_base_url_is_required() -> None:
    with pytest.raises(ValueError):
        OrionBeltClient("   ")


def test_base_url_trailing_slashes_are_dropped(server: RecordingServer) -> None:
    c = OrionBeltClient(server.url + "///", api_key="k")
    c.get_setup_status()
    assert server.last.target == "/api/v1/setup/status"


def test_url_keeps_absolute_and_api_paths() -> None:
    c = OrionBeltClient("https://gw.example.com")
    assert c.url("https://other/x") == "https://other/x"
    assert c.url("/api/v2/x") == "https://gw.example.com/api/v2/x"
    assert c.url("machines") == "https://gw.example.com/api/v1/machines"


def test_api_key_wins_over_session_token_and_bearer_is_added(server: RecordingServer) -> None:
    c = OrionBeltClient(server.url, api_key=" k ", session_token="s", bearer_token="b")
    c.get_current_user()
    headers = server.last.headers
    assert headers["x-api-key"] == "k"
    assert "x-session-token" not in headers
    assert headers["authorization"] == "Bearer b"


def test_session_token_is_used_without_api_key(server: RecordingServer) -> None:
    c = OrionBeltClient(server.url, session_token="s")
    c.get_current_user()
    assert server.last.headers["x-session-token"] == "s"


def test_blank_credentials_are_not_sent(server: RecordingServer) -> None:
    c = OrionBeltClient(server.url, api_key="  ")
    c.set_bearer_token("")
    c.get_current_user()
    assert "x-api-key" not in server.last.headers
    assert "authorization" not in server.last.headers


def test_user_agent_and_custom_headers_are_sent(server: RecordingServer) -> None:
    c = OrionBeltClient(server.url, user_agent=" automation/1.0 ", headers={"X-Trace": "t1"})
    c.get_gateway_info()
    assert server.last.headers["user-agent"] == "automation/1.0"
    assert server.last.headers["x-trace"] == "t1"


@pytest.mark.parametrize(
    ("body", "message"),
    [
        ('{"error":"machine not found"}', "machine not found"),
        ('{"message":"nope"}', "nope"),
        ("plain failure\n", "plain failure"),
        ("[1]", "[1]"),
        ('{"code":7}', '{"code":7}'),
    ],
)
def test_errors_carry_status_and_server_message(
    server: RecordingServer, client: OrionBeltClient, body: str, message: str
) -> None:
    server.reply(404, body)
    with pytest.raises(OrionBeltError) as info:
        client.get_machine("m1")
    assert info.value.status_code == 404
    assert info.value.message == message
    assert str(info.value) == f"api error (404): {message}"


def test_error_without_body_formats_status_only(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(500, "")
    with pytest.raises(OrionBeltError) as info:
        client.request_bytes("GET", "/sessions/s1/content")
    assert str(info.value) == "api error (500)"


def test_ids_cannot_escape_their_path_segment(server: RecordingServer, client: OrionBeltClient) -> None:
    client.get_user("../admin/users?x=1")
    assert server.last.target == "/api/v1/users/..%2Fadmin%2Fusers%3Fx%3D1"


def test_plugin_webhook_keeps_subpath_slashes_and_escapes_segments(server: RecordingServer, client: OrionBeltClient) -> None:
    client.plugin_webhook("my plugin", "/a/b c", method="GET")
    assert server.last.method == "GET"
    assert server.last.target == "/api/v1/public/plugins/my%20plugin/a/b%20c"


def test_query_omits_defaults_and_encodes_values(server: RecordingServer, client: OrionBeltClient) -> None:
    client.list_audit_logs()
    assert server.last.target == "/api/v1/audit-logs"
    client.explain_access("web 1", "root", "scp", "alice")
    assert query_of(server.last.target) == {
        "machine": ["web 1"],
        "remote_user": ["root"],
        "access_type": ["scp"],
        "user": ["alice"],
    }


def test_delete_machine_archive_flag(server: RecordingServer, client: OrionBeltClient) -> None:
    client.delete_machine("m1")
    assert server.last.target == "/api/v1/admin/machines/m1"
    client.delete_machine("m1", archive=True)
    assert server.last.target == "/api/v1/admin/machines/m1?archive=true"


def test_export_report_merges_params_and_format(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, b"a,b\n1,2\n", "text/csv")
    data = client.export_report("pci-admin-activity", "csv", {"from": "2026-01-01", "to": "2026-02-01"})
    assert data == b"a,b\n1,2\n"
    assert query_of(server.last.target) == {"from": ["2026-01-01"], "to": ["2026-02-01"], "format": ["csv"]}


def test_ssh_login_signs_the_challenge_and_stores_session(server: RecordingServer) -> None:
    server.reply(200, LOGIN_REPLY)
    c = OrionBeltClient(server.url)
    result = c.login_with_ssh_session("alice", FakeSigner(), " 123456 ")
    assert result["session_token"] == "s1"
    challenge, login = server.requests
    assert challenge.target == "/api/v1/public/auth/challenge"
    body = json.loads(login.body)
    assert body["public_key"] == FakeSigner.public_key
    assert base64.b64decode(body["signature"]) == b"sig:c1"
    assert body["totp_code"] == "123456"
    assert c.session_token == "s1"
    assert c.bearer_token == "a1"


def test_ssh_login_omits_blank_totp(server: RecordingServer) -> None:
    server.reply(200, LOGIN_REPLY)
    OrionBeltClient(server.url).login_with_ssh_key("alice", FakeSigner())
    assert "totp_code" not in json.loads(server.last.body)


def test_login_helpers_store_their_credential(server: RecordingServer) -> None:
    server.reply(200, LOGIN_REPLY)
    c = OrionBeltClient(server.url)
    c.login_with_ssh_key("u", FakeSigner())
    assert c.api_key == "k1"
    c.login_with_ssh_jwt("u", FakeSigner())
    assert c.bearer_token == "a1"
    c.set_session_token(None)
    c.login_with_password("u", "pw")
    assert c.session_token == "s1"


@pytest.mark.parametrize(
    "login",
    [
        lambda c: c.login_with_password("u", "pw"),
        lambda c: c.login_with_ssh_session("u", FakeSigner()),
        lambda c: c.login_with_ssh_key("u", FakeSigner()),
        lambda c: c.login_with_ssh_jwt("u", FakeSigner()),
    ],
)
def test_login_without_credential_in_reply_fails(server: RecordingServer, login: Callable[[OrionBeltClient], object]) -> None:
    server.routes["/api/v1/public/auth/challenge"] = b'{"challenge":"c1"}'
    server.reply(200, "{}")
    with pytest.raises(ValueError):
        login(OrionBeltClient(server.url))


def test_empty_challenge_fails(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, '{"challenge":""}')
    with pytest.raises(ValueError):
        client.issue_challenge("u")


def test_session_reply_without_tokens_leaves_client_unchanged(server: RecordingServer) -> None:
    server.reply(200, "{}")
    c = OrionBeltClient(server.url, session_token="old")
    c.redeem_browser_bootstrap("code")
    assert c.session_token == "old"


def test_null_lists_become_empty(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, "null")
    assert client.list_machines() == []
    server.reply(200, '{"api_keys":null}')
    assert client.list_api_keys() == []
    server.reply(200, "[1]")
    assert client.list_plugins() == []


def test_list_files_defaults_entries(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, '{"path":"/","raw":"total 0"}')
    assert client.list_files("web")["entries"] == []
    assert query_of(server.last.target) == {"machine": ["web"]}


def test_upload_is_multipart_with_fields_and_safe_filename(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, '{"message":"ok","path":"/tmp/x","size":5}')
    result = client.upload_file("web", "/tmp", 'a"b\r\n.txt', b"hello")
    assert result["size"] == 5
    sent = server.last
    assert sent.headers["content-type"].startswith("multipart/form-data; boundary=")
    body = sent.body.decode()
    assert 'name="machine"\r\n\r\nweb\r\n' in body
    assert 'filename="a\\"b.txt"' in body
    assert 'name="user"' not in body
    assert "\r\n\r\nhello\r\n" in body


def test_make_dir_sends_user_only_when_set(server: RecordingServer, client: OrionBeltClient) -> None:
    client.make_dir("web", "/tmp/x")
    assert json.loads(server.last.body) == {"machine": "web", "path": "/tmp/x"}


def test_scalar_results(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, '{"unread":7}')
    assert client.unread_notification_count() == 7
    server.reply(200, '{"output":"drained"}')
    assert client.send_agent_command("m1", "drain") == "drained"
    server.reply(200, '{"id":"cred-1"}')
    assert client.webauthn_register_finish({}) == "cred-1"
    server.reply(200, '{"plugin":{"name":"slack","enabled":true}}')
    assert client.enable_plugin("slack")["enabled"] is True


def test_empty_response_body_is_none(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, "")
    assert client.request("POST", "/logout") is None


def test_put_notification_template_uses_path_event(server: RecordingServer, client: OrionBeltClient) -> None:
    client.put_notification_template("access_request.approved", {"event_type": "other", "title": "T"})
    assert json.loads(server.last.body)["event_type"] == "access_request.approved"


def test_get_machine_by_name(server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, '[{"id":"m1","name":"db"},{"id":"m2","name":"web"}]')
    assert client.get_machine_by_name("web")["id"] == "m2"
    with pytest.raises(LookupError):
        client.get_machine_by_name("missing")


def test_websocket_urls_follow_scheme() -> None:
    assert (
        OrionBeltClient("https://gw.example.com").terminal_ws_url("web") == "wss://gw.example.com/api/v1/terminal/ws?machine=web"
    )
    assert (
        OrionBeltClient("http://localhost:8080").session_watch_ws_url("s 1") == "ws://localhost:8080/api/v1/sessions/s%201/watch"
    )


def test_auth_headers_for_websockets() -> None:
    assert OrionBeltClient("http://x", session_token="s", bearer_token="b").auth_headers() == {
        "X-Session-Token": "s",
        "Authorization": "Bearer b",
    }


def test_version_is_exposed() -> None:
    assert isinstance(__version__, str) and __version__


def test_logout_forgets_session_credentials(server: RecordingServer) -> None:
    c = OrionBeltClient(server.url, api_key="k", session_token="s", bearer_token="b")
    c.logout()
    assert server.last.target == "/api/v1/logout"
    assert (c.api_key, c.session_token, c.bearer_token) == ("k", None, None)


def test_failed_logout_keeps_credentials(server: RecordingServer) -> None:
    server.reply(401, '{"error":"unauthorized"}')
    c = OrionBeltClient(server.url, session_token="s")
    with pytest.raises(OrionBeltError):
        c.logout()
    assert c.session_token == "s"
