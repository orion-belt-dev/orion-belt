# orion-belt-sdk

Python client for the [Orion Belt](https://github.com/orion-belt-dev/orion-belt)
API, for scripts and automation that glue Orion Belt into other tooling.

- Covers every `/api/v1` endpoint the server exposes. A test checks the client
  against the server's route table, so new endpoints can't be missed.
- Standard library only: no runtime dependencies.
- Typed (`py.typed`): responses are `TypedDict`s, so fields the server adds in
  later versions still reach you.
- Python 3.11+.

## Install

```sh
pip install orion-belt-sdk
```

## Use

```python
from orion_belt_sdk import OrionBeltClient, OrionBeltError

client = OrionBeltClient("https://pam.example.com", api_key="...")

for machine in client.list_machines():
    print(machine["name"], machine["is_active"])

request = client.create_access_request(
    {"machine_id": machine["id"], "reason": "deploy 1.4.2", "duration": 3600, "remote_users": ["deploy"]}
)

try:
    client.get_machine("does-not-exist")
except OrionBeltError as err:
    print(err.status_code, err.message)
```

Method names follow the [Go SDK](https://github.com/orion-belt-dev/orion-belt/tree/master/pkg/sdk)
in snake_case (`ListMachines` becomes `list_machines`). For an endpoint the
client has no method for yet, use `client.request(method, path, body)`.

### Authentication

Pass one of `api_key`, `session_token` or `bearer_token`, or log in. The login
helpers store the credential they get back on the client.

| Helper | Credential |
|--------|------------|
| `login_with_ssh_key(username, signer)` | API key |
| `login_with_ssh_session(username, signer)` | session token (+ JWT) |
| `login_with_ssh_jwt(username, signer)` | JWT bearer token |
| `login_with_password(username, password, totp_code)` | session token (+ JWT) |

A signer proves you hold an SSH key registered to the user. It needs a
`public_key` (the authorized_keys line) and a `sign(data)` method that returns
the signature format and the raw signature. With the `cryptography` package:

```python
from cryptography.hazmat.primitives import serialization

from orion_belt_sdk import OrionBeltClient

key = serialization.load_ssh_private_key(open("id_ed25519", "rb").read(), password=None)


class Signer:
    public_key = key.public_key().public_bytes(serialization.Encoding.OpenSSH, serialization.PublicFormat.OpenSSH).decode()

    def sign(self, data: bytes) -> tuple[str, bytes]:
        return "ssh-ed25519", key.sign(data)


client = OrionBeltClient("https://pam.example.com")
client.login_with_ssh_key("alice", Signer())
```

For RSA keys, return `"rsa-sha2-256"` and a PKCS#1 v1.5 SHA-256 signature.

`logout()` ends the HTTP session and clears the session and bearer tokens from
the client. The server does not revoke a JWT at logout; it stays valid until
it expires.

### WebSockets

`terminal_ws_url()` and `session_watch_ws_url()` return the `ws://`/`wss://`
URL. Open it with any WebSocket library and send `client.auth_headers()` with
the upgrade request.

## Develop

From `sdk/python`, with [uv](https://docs.astral.sh/uv/):

```sh
uv sync --locked
uv run ruff check && uv run ruff format --check
uv run mypy
uv run pytest          # includes branch coverage, fails under 90%
```

If the server adds an endpoint, regenerate the route list
(`go test ./pkg/api -run TestRoutesManifest -update-routes` from the repo root).
Then add the method and its entry in `tests/test_contract.py`.

## Release

Bump `version` in `pyproject.toml` and push a tag `sdk-python-v<version>`. The
`SDK release` workflow tests, builds and publishes to PyPI.

## License

Apache License 2.0 with the Commons Clause condition. See [LICENSE](https://github.com/orion-belt-dev/orion-belt/blob/master/LICENSE).
