"""Wire contract for every SDK method, checked against the server's route list.

sdk/routes.json is generated from the Go router (pkg/api routes_manifest_test.go),
so a route added to the server without an SDK method fails
test_every_route_is_covered.
"""

from __future__ import annotations

import json
import re
import urllib.parse
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import pytest

from orion_belt_sdk import OrionBeltClient

from .conftest import RecordingServer

MANIFEST = Path(__file__).resolve().parents[2] / "routes.json"

# One body that satisfies every login flow: the challenge step reads
# "challenge", the login step reads its token.
LOGIN_REPLY = '{"challenge":"c1","session_token":"s1","access_token":"a1","api_key":"k1"}'


class FakeSigner:
    public_key = "ssh-ed25519 AAAAC3Nza test"

    def sign(self, data: bytes) -> tuple[str, bytes]:
        return "ssh-ed25519", b"sig:" + data


@dataclass(frozen=True)
class Endpoint:
    name: str
    call: Callable[[OrionBeltClient], Any]
    method: str
    route: str  # as in routes.json
    public: bool = False  # sent without credentials
    body_has: str = ""  # substring the request body must contain
    response: str = "{}"
    websocket: bool = False  # call returns a URL instead of sending a request


S = FakeSigner()

ENDPOINTS: list[Endpoint] = [
    Endpoint("get_gateway_info", lambda c: c.get_gateway_info(), "GET", "/api/v1/gateway-info", public=True),
    Endpoint("get_version", lambda c: c.get_version(), "GET", "/api/v1/version", public=True),
    Endpoint(
        "get_openapi_spec", lambda c: c.get_openapi_spec(), "GET", "/api/v1/openapi.yaml", public=True, response="openapi: 3.0.3"
    ),
    Endpoint("get_openapi_index", lambda c: c.get_openapi_index(), "GET", "/api/v1/openapi.json", public=True),
    Endpoint(
        "register_agent",
        lambda c: c.register_agent({"name": "a", "hostname": "h", "port": 22, "public_key": "k"}),
        "POST",
        "/api/v1/public/register/agent",
        body_has='"hostname": "h"',
    ),
    Endpoint(
        "register_client",
        lambda c: c.register_client({"username": "u", "email": "e", "public_key": "k"}),
        "POST",
        "/api/v1/public/register/client",
        body_has='"username": "u"',
    ),
    Endpoint(
        "issue_challenge",
        lambda c: c.issue_challenge("u"),
        "POST",
        "/api/v1/public/auth/challenge",
        public=True,
        body_has='"username": "u"',
        response=LOGIN_REPLY,
    ),
    Endpoint(
        "login_with_ssh_session",
        lambda c: c.login_with_ssh_session("u", S),
        "POST",
        "/api/v1/public/login",
        public=True,
        body_has='"signature_format": "ssh-ed25519"',
        response=LOGIN_REPLY,
    ),
    Endpoint(
        "login_with_ssh_key",
        lambda c: c.login_with_ssh_key("u", S, "123456"),
        "POST",
        "/api/v1/public/login/key",
        public=True,
        body_has='"totp_code": "123456"',
        response=LOGIN_REPLY,
    ),
    Endpoint(
        "login_with_ssh_jwt",
        lambda c: c.login_with_ssh_jwt("u", S),
        "POST",
        "/api/v1/public/login/token",
        public=True,
        body_has='"challenge": "c1"',
        response=LOGIN_REPLY,
    ),
    Endpoint(
        "login_with_password",
        lambda c: c.login_with_password("u", "pw", "1"),
        "POST",
        "/api/v1/public/login/password",
        public=True,
        body_has='"password": "pw"',
        response=LOGIN_REPLY,
    ),
    Endpoint(
        "redeem_browser_bootstrap",
        lambda c: c.redeem_browser_bootstrap("code"),
        "POST",
        "/api/v1/public/auth/browser-bootstrap/redeem",
        public=True,
        body_has='"code": "code"',
        response=LOGIN_REPLY,
    ),
    Endpoint(
        "webauthn_login_begin",
        lambda c: c.webauthn_login_begin("u"),
        "POST",
        "/api/v1/public/webauthn/login/begin",
        public=True,
        body_has='"username": "u"',
    ),
    Endpoint(
        "webauthn_login_finish",
        lambda c: c.webauthn_login_finish("u", {"id": "x"}),
        "POST",
        "/api/v1/public/webauthn/login/finish",
        public=True,
        body_has='"response": {"id": "x"}',
        response=LOGIN_REPLY,
    ),
    Endpoint(
        "plugin_webhook",
        lambda c: c.plugin_webhook("slack", "events/x", body={"a": 1}),
        "ANY",
        "/api/v1/public/plugins/:name/*proxyPath",
        public=True,
        body_has='"a": 1',
    ),
    Endpoint("logout", lambda c: c.logout(), "POST", "/api/v1/logout"),
    Endpoint("get_current_user", lambda c: c.get_current_user(), "GET", "/api/v1/auth/me"),
    Endpoint("request_browser_bootstrap", lambda c: c.request_browser_bootstrap(), "POST", "/api/v1/auth/browser-bootstrap"),
    Endpoint("set_password", lambda c: c.set_password("pw", "1"), "POST", "/api/v1/auth/password", body_has='"password": "pw"'),
    Endpoint("clear_password", lambda c: c.clear_password("1"), "DELETE", "/api/v1/auth/password", body_has='"totp_code": "1"'),
    Endpoint("mfa_enroll", lambda c: c.mfa_enroll(), "POST", "/api/v1/mfa/enroll"),
    Endpoint("mfa_confirm", lambda c: c.mfa_confirm("1"), "POST", "/api/v1/mfa/confirm", body_has='"code": "1"'),
    Endpoint("mfa_disable", lambda c: c.mfa_disable("1"), "POST", "/api/v1/mfa/disable", body_has='"code": "1"'),
    Endpoint("mfa_status", lambda c: c.mfa_status(), "GET", "/api/v1/mfa/status"),
    Endpoint("webauthn_register_begin", lambda c: c.webauthn_register_begin(), "POST", "/api/v1/webauthn/register/begin"),
    Endpoint(
        "webauthn_register_finish",
        lambda c: c.webauthn_register_finish({"id": "x"}),
        "POST",
        "/api/v1/webauthn/register/finish",
        body_has='"id": "x"',
    ),
    Endpoint("webauthn_credentials", lambda c: c.webauthn_credentials(), "GET", "/api/v1/webauthn/credentials", response="[]"),
    Endpoint(
        "webauthn_delete_credential", lambda c: c.webauthn_delete_credential("w1"), "DELETE", "/api/v1/webauthn/credentials/:id"
    ),
    Endpoint("list_ssh_keys", lambda c: c.list_ssh_keys(), "GET", "/api/v1/ssh-keys", response="[]"),
    Endpoint(
        "add_ssh_key",
        lambda c: c.add_ssh_key("laptop", "ssh-ed25519 AAA"),
        "POST",
        "/api/v1/ssh-keys",
        body_has='"name": "laptop"',
    ),
    Endpoint("delete_ssh_key", lambda c: c.delete_ssh_key("k1"), "DELETE", "/api/v1/ssh-keys/:id"),
    Endpoint(
        "create_api_key",
        lambda c: c.create_api_key({"name": "ci", "expires_in": 30}),
        "POST",
        "/api/v1/api-keys",
        body_has='"expires_in": 30',
    ),
    Endpoint("list_api_keys", lambda c: c.list_api_keys(), "GET", "/api/v1/api-keys", response='{"api_keys":[]}'),
    Endpoint("revoke_api_key", lambda c: c.revoke_api_key("k1"), "POST", "/api/v1/api-keys/:id/revoke"),
    Endpoint("delete_api_key", lambda c: c.delete_api_key("k1"), "DELETE", "/api/v1/api-keys/:id"),
    Endpoint("list_notifications", lambda c: c.list_notifications(5), "GET", "/api/v1/notifications", response="[]"),
    Endpoint(
        "unread_notification_count",
        lambda c: c.unread_notification_count(),
        "GET",
        "/api/v1/notifications/unread-count",
        response='{"unread":3}',
    ),
    Endpoint("mark_notification_read", lambda c: c.mark_notification_read("n1"), "POST", "/api/v1/notifications/:id/read"),
    Endpoint("mark_all_notifications_read", lambda c: c.mark_all_notifications_read(), "POST", "/api/v1/notifications/read-all"),
    Endpoint("get_notification_prefs", lambda c: c.get_notification_prefs(), "GET", "/api/v1/notifications/prefs"),
    Endpoint(
        "put_notification_prefs",
        lambda c: c.put_notification_prefs({"email_enabled": True}),
        "PUT",
        "/api/v1/notifications/prefs",
        body_has='"email_enabled": true',
    ),
    Endpoint("list_users", lambda c: c.list_users(), "GET", "/api/v1/users", response="[]"),
    Endpoint("get_user", lambda c: c.get_user("u1"), "GET", "/api/v1/users/:id"),
    Endpoint(
        "create_user",
        lambda c: c.create_user({"username": "bob", "email": "b@x"}),
        "POST",
        "/api/v1/admin/users",
        body_has='"username": "bob"',
    ),
    Endpoint(
        "update_user",
        lambda c: c.update_user("u1", {"role": "auditor"}),
        "PUT",
        "/api/v1/admin/users/:id",
        body_has='"role": "auditor"',
    ),
    Endpoint("delete_user", lambda c: c.delete_user("u1"), "DELETE", "/api/v1/admin/users/:id"),
    Endpoint("list_machines", lambda c: c.list_machines(), "GET", "/api/v1/machines", response="[]"),
    Endpoint("get_machine", lambda c: c.get_machine("m1"), "GET", "/api/v1/machines/:id"),
    Endpoint(
        "create_machine",
        lambda c: c.create_machine({"name": "web", "hostname": "10.0.0.1"}),
        "POST",
        "/api/v1/admin/machines",
        body_has='"name": "web"',
    ),
    Endpoint(
        "update_machine",
        lambda c: c.update_machine("m1", {"is_active": False}),
        "PUT",
        "/api/v1/admin/machines/:id",
        body_has='"is_active": false',
    ),
    Endpoint("delete_machine", lambda c: c.delete_machine("m1", archive=True), "DELETE", "/api/v1/admin/machines/:id"),
    Endpoint(
        "kube_reconcile(dry_run)",
        lambda c: c.kube_reconcile("m1", dry_run=True),
        "GET",
        "/api/v1/admin/machines/:id/kubernetes/reconcile",
    ),
    Endpoint("kube_reconcile", lambda c: c.kube_reconcile("m1"), "POST", "/api/v1/admin/machines/:id/kubernetes/reconcile"),
    Endpoint(
        "get_user_permissions", lambda c: c.get_user_permissions("u1"), "GET", "/api/v1/permissions/user/:id", response="[]"
    ),
    Endpoint(
        "get_machine_permissions",
        lambda c: c.get_machine_permissions("m1"),
        "GET",
        "/api/v1/permissions/machine/:id",
        response="[]",
    ),
    Endpoint("list_all_permissions", lambda c: c.list_all_permissions(10), "GET", "/api/v1/admin/permissions", response="[]"),
    Endpoint(
        "grant_permission",
        lambda c: c.grant_permission({"user_id": "u1", "machine_id": "m1", "access_type": "ssh"}),
        "POST",
        "/api/v1/admin/permissions",
        body_has='"access_type": "ssh"',
    ),
    Endpoint(
        "update_permission",
        lambda c: c.update_permission("p1", {"remote_users": ["root"]}),
        "PATCH",
        "/api/v1/admin/permissions/:id",
        body_has='"remote_users": ["root"]',
    ),
    Endpoint("revoke_permission", lambda c: c.revoke_permission("p1"), "DELETE", "/api/v1/admin/permissions/:id"),
    Endpoint(
        "bulk_grant_permissions",
        lambda c: c.bulk_grant_permissions({"user_ids": ["u1"], "machine_ids": ["m1"], "access_type": "ssh"}),
        "POST",
        "/api/v1/admin/permissions/bulk",
        body_has='"user_ids": ["u1"]',
    ),
    Endpoint(
        "bulk_revoke_permissions",
        lambda c: c.bulk_revoke_permissions(["p1", "p2"]),
        "POST",
        "/api/v1/admin/permissions/bulk-revoke",
        body_has='"ids": ["p1", "p2"]',
    ),
    Endpoint(
        "effective_permissions",
        lambda c: c.effective_permissions(user_id="u1"),
        "GET",
        "/api/v1/admin/permissions/effective",
        response="[]",
    ),
    Endpoint("list_capabilities", lambda c: c.list_capabilities("all"), "GET", "/api/v1/capabilities"),
    Endpoint("explain_access", lambda c: c.explain_access("web", "root"), "GET", "/api/v1/access/explain"),
    Endpoint(
        "list_access_requests", lambda c: c.list_access_requests("pending"), "GET", "/api/v1/access-requests", response="[]"
    ),
    Endpoint(
        "create_access_request",
        lambda c: c.create_access_request({"machine_id": "m1", "reason": "deploy", "duration": 3600}),
        "POST",
        "/api/v1/access-requests",
        body_has='"reason": "deploy"',
    ),
    Endpoint("get_access_request", lambda c: c.get_access_request("r1"), "GET", "/api/v1/access-requests/:id"),
    Endpoint(
        "list_pending_access_requests",
        lambda c: c.list_pending_access_requests(),
        "GET",
        "/api/v1/admin/access-requests/pending",
        response="[]",
    ),
    Endpoint(
        "approve_access_request",
        lambda c: c.approve_access_request("r1", "u9"),
        "POST",
        "/api/v1/admin/access-requests/:id/approve",
        body_has='"reviewer_id": "u9"',
    ),
    Endpoint(
        "reject_access_request",
        lambda c: c.reject_access_request("r1", "u9"),
        "POST",
        "/api/v1/admin/access-requests/:id/reject",
        body_has='"reviewer_id": "u9"',
    ),
    Endpoint("get_risk_policy", lambda c: c.get_risk_policy(), "GET", "/api/v1/admin/risk-policy"),
    Endpoint(
        "put_risk_policy",
        lambda c: c.put_risk_policy({"auto_approve": {"enabled": True}}),
        "PUT",
        "/api/v1/admin/risk-policy",
        body_has='"auto_approve"',
    ),
    Endpoint("list_sessions", lambda c: c.list_sessions("active"), "GET", "/api/v1/sessions", response="[]"),
    Endpoint("list_active_sessions", lambda c: c.list_active_sessions(), "GET", "/api/v1/sessions/active", response="[]"),
    Endpoint("get_session", lambda c: c.get_session("s1"), "GET", "/api/v1/sessions/:id"),
    Endpoint("get_session_content", lambda c: c.get_session_content("s1"), "GET", "/api/v1/sessions/:id/content", response="raw"),
    Endpoint("list_audit_logs", lambda c: c.list_audit_logs(50), "GET", "/api/v1/audit-logs", response="[]"),
    Endpoint(
        "export_report",
        lambda c: c.export_report("soc2-access", "csv", {"from": "2026-01-01"}),
        "GET",
        "/api/v1/reports/:name/export",
        response="a,b",
    ),
    Endpoint("get_usage_dashboard", lambda c: c.get_usage_dashboard(24, 5), "GET", "/api/v1/dashboard/usage"),
    Endpoint("list_anomalies", lambda c: c.list_anomalies(10, "u1"), "GET", "/api/v1/anomalies", response="[]"),
    Endpoint("get_setup_status", lambda c: c.get_setup_status(), "GET", "/api/v1/setup/status"),
    Endpoint("get_notification_policy", lambda c: c.get_notification_policy(), "GET", "/api/v1/admin/notifications/policy"),
    Endpoint(
        "put_notification_policy",
        lambda c: c.put_notification_policy({"allowed_channels": ["in_app"]}),
        "PUT",
        "/api/v1/admin/notifications/policy",
        body_has='"allowed_channels": ["in_app"]',
    ),
    Endpoint(
        "list_notification_templates",
        lambda c: c.list_notification_templates(),
        "GET",
        "/api/v1/admin/notifications/templates",
        response='{"templates":[]}',
    ),
    Endpoint(
        "put_notification_template",
        lambda c: c.put_notification_template("access_request.approved", {"title": "T"}),
        "PUT",
        "/api/v1/admin/notifications/templates/:event",
        body_has='"event_type": "access_request.approved"',
    ),
    Endpoint(
        "delete_notification_template",
        lambda c: c.delete_notification_template("access_request.approved"),
        "DELETE",
        "/api/v1/admin/notifications/templates/:event",
    ),
    Endpoint("list_plugins", lambda c: c.list_plugins(), "GET", "/api/v1/admin/plugins", response='{"plugins":[]}'),
    Endpoint(
        "update_plugin_config",
        lambda c: c.update_plugin_config("slack", True, {"token": "x"}),
        "PUT",
        "/api/v1/admin/plugins/:name/config",
        body_has='"enabled": true',
    ),
    Endpoint(
        "enable_plugin",
        lambda c: c.enable_plugin("slack"),
        "POST",
        "/api/v1/admin/plugins/:name/enable",
        response='{"plugin":{"name":"slack"}}',
    ),
    Endpoint(
        "disable_plugin",
        lambda c: c.disable_plugin("slack"),
        "POST",
        "/api/v1/admin/plugins/:name/disable",
        response='{"plugin":{"name":"slack"}}',
    ),
    Endpoint(
        "list_connected_agents",
        lambda c: c.list_connected_agents(),
        "GET",
        "/api/v1/admin/agents/connected",
        response='{"agents":["m1"]}',
    ),
    Endpoint(
        "send_agent_command",
        lambda c: c.send_agent_command("m1", "reload"),
        "POST",
        "/api/v1/admin/agents/:machine_id/command",
        body_has='"command": "reload"',
        response='{"output":"ok"}',
    ),
    Endpoint("disconnect_agent", lambda c: c.disconnect_agent("m1"), "POST", "/api/v1/admin/agents/:machine_id/disconnect"),
    Endpoint(
        "generate_agent_install_script",
        lambda c: c.generate_agent_install_script(
            {"name": "a", "os": "linux", "gateway_host": "gw", "package_base_url": "https://pkgs"}
        ),
        "POST",
        "/api/v1/admin/agents/install-script",
        body_has='"gateway_host": "gw"',
    ),
    Endpoint("get_trusted_ca", lambda c: c.get_trusted_ca(), "GET", "/api/v1/ssh-cert/ca"),
    Endpoint(
        "issue_user_cert",
        lambda c: c.issue_user_cert("ssh-ed25519 AAA", 8),
        "POST",
        "/api/v1/ssh-cert",
        body_has='"ttl_hours": 8',
    ),
    Endpoint("export_ca", lambda c: c.export_ca(), "GET", "/api/v1/admin/ca/export"),
    Endpoint(
        "list_ssh_certificates",
        lambda c: c.list_ssh_certificates("user"),
        "GET",
        "/api/v1/admin/ssh-certificates",
        response='{"certificates":[]}',
    ),
    Endpoint(
        "revoke_ssh_certificate",
        lambda c: c.revoke_ssh_certificate("42", "lost"),
        "POST",
        "/api/v1/admin/ssh-certificates/:serial/revoke",
        body_has='"reason": "lost"',
    ),
    Endpoint(
        "list_files", lambda c: c.list_files("web", "/etc", "root"), "GET", "/api/v1/files/list", response='{"path":"/etc"}'
    ),
    Endpoint(
        "download_file", lambda c: c.download_file("web", "/etc/hosts"), "GET", "/api/v1/files/download", response="127.0.0.1"
    ),
    Endpoint(
        "upload_file",
        lambda c: c.upload_file("web", "/tmp", "a.txt", b"hello", "root"),
        "POST",
        "/api/v1/files/upload",
        body_has='filename="a.txt"',
    ),
    Endpoint(
        "make_dir", lambda c: c.make_dir("web", "/tmp/x", "root"), "POST", "/api/v1/files/mkdir", body_has='"path": "/tmp/x"'
    ),
    Endpoint("delete_file", lambda c: c.delete_file("web", "/tmp/x"), "DELETE", "/api/v1/files"),
    Endpoint("terminal_ws_url", lambda c: c.terminal_ws_url("web", "root"), "GET", "/api/v1/terminal/ws", websocket=True),
    Endpoint("session_watch_ws_url", lambda c: c.session_watch_ws_url("s1"), "GET", "/api/v1/sessions/:id/watch", websocket=True),
]


def route_pattern(route: str) -> re.Pattern[str]:
    parts = []
    for seg in route.strip("/").split("/"):
        if seg.startswith(":"):
            parts.append("[^/]+")
        elif seg.startswith("*"):
            parts.append(".+")
        else:
            parts.append(re.escape(seg))
    return re.compile("^/" + "/".join(parts) + "$")


def load_manifest() -> set[tuple[str, str]]:
    return {(r["method"], r["path"]) for r in json.loads(MANIFEST.read_text())}


def test_every_route_is_covered() -> None:
    covered = {(e.method, e.route) for e in ENDPOINTS}
    missing = sorted(load_manifest() - covered)
    assert not missing, f"routes with no SDK method: {missing}"


def test_no_endpoint_targets_an_unknown_route() -> None:
    unknown = sorted({(e.method, e.route) for e in ENDPOINTS} - load_manifest())
    assert not unknown, f"SDK methods for routes the server does not have: {unknown}"


@pytest.mark.parametrize("ep", ENDPOINTS, ids=lambda e: e.name)
def test_endpoint_wire_contract(ep: Endpoint, server: RecordingServer, client: OrionBeltClient) -> None:
    server.reply(200, ep.response)
    result = ep.call(client)

    if ep.websocket:
        assert isinstance(result, str)
        assert result.startswith("ws://")
        assert route_pattern(ep.route).match(urllib.parse.urlsplit(result).path)
        assert not server.requests
        return

    sent = server.last
    if ep.method != "ANY":
        assert sent.method == ep.method
    path = sent.target.split("?", 1)[0]
    assert route_pattern(ep.route).match(path), f"{sent.method} {sent.target} does not match {ep.route}"
    if ep.public:
        assert "x-api-key" not in sent.headers
    else:
        assert sent.headers.get("x-api-key") == "test-key"
    if ep.body_has:
        assert ep.body_has in sent.body.decode(), sent.body
