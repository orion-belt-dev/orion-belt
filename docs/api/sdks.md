---
sidebar_position: 23
title: SDKs
description: Python, TypeScript and Go clients for the Orion Belt API.
---

# SDKs

:::info[New in 1.5.0]
The Python and TypeScript SDKs cover every `/api/v1` endpoint of 1.5.0.
:::

| Language | Package | Install |
| --- | --- | --- |
| Python 3.11+ | [`orion-belt-sdk`](https://pypi.org/project/orion-belt-sdk/) | `pip install orion-belt-sdk` |
| TypeScript / JavaScript | [`@orion-belt/sdk`](https://www.npmjs.com/package/@orion-belt/sdk) | `npm install @orion-belt/sdk` |
| Go | [`pkg/sdk`](https://github.com/orion-belt-dev/orion-belt/tree/master/pkg/sdk) | `go get github.com/orion-belt-dev/orion-belt/pkg/sdk` |

The Go SDK is the reference: the other two use its method names, in
snake_case for Python and camelCase for TypeScript. Neither has runtime
dependencies. The Python client uses the standard library only; the
TypeScript client uses the platform `fetch` and runs in Node 20+, Deno, Bun
and browsers.

The server's route table is checked into the repository as
[`sdk/routes.json`](https://github.com/orion-belt-dev/orion-belt/blob/master/sdk/routes.json).
CI fails if the router and that file differ, or if either SDK has no method
for a route, so a new endpoint can't ship without both clients.

## Python

```python
from orion_belt_sdk import OrionBeltClient, OrionBeltError

client = OrionBeltClient("https://orion.example.com", api_key="...")

for machine in client.list_machines():
    print(machine["name"], machine["is_active"])

client.create_access_request(
    {"machine_id": machine["id"], "reason": "deploy", "duration": 3600, "remote_users": ["deploy"]}
)

try:
    client.get_machine("does-not-exist")
except OrionBeltError as err:
    print(err.status_code, err.message)
```

Responses are `TypedDict`s (`py.typed`), so fields a newer server adds still
reach you.

## TypeScript

```ts
import { ApiError, OrionBeltClient } from '@orion-belt/sdk';

const client = new OrionBeltClient({ baseUrl: 'https://orion.example.com', apiKey: '...' });

for (const machine of await client.listMachines()) {
  console.log(machine.name, machine.is_active);
}

try {
  await client.getMachine('does-not-exist');
} catch (err) {
  if (err instanceof ApiError) console.log(err.statusCode, err.serverMessage);
}
```

Request and response fields keep the API's snake_case names.

## Authentication

Pass an API key, session token or bearer token when you create the client, or
log in with one of the helpers. Each helper stores the credential it gets back
on the client.

| Python | TypeScript | Credential |
| --- | --- | --- |
| `login_with_ssh_key` | `loginWithSshKey` | API key |
| `login_with_ssh_session` | `loginWithSshSession` | Session token and JWT |
| `login_with_ssh_jwt` | `loginWithSshJwt` | JWT bearer token |
| `login_with_password` | `loginWithPassword` | Session token and JWT |

The SSH helpers take a signer: the user's registered public key and a
`sign(data)` function. The package READMEs show one for ed25519 and RSA keys:
[Python](https://github.com/orion-belt-dev/orion-belt/blob/master/sdk/python/README.md),
[TypeScript](https://github.com/orion-belt-dev/orion-belt/blob/master/sdk/js/README.md).

`logout()` ends the HTTP session and clears the tokens from the client. The
server does not revoke a JWT at logout; it stays valid until it expires.

## Errors

Failed calls raise `OrionBeltError` (Python) or throw `ApiError`
(TypeScript) with the HTTP status and the server's message. The error kinds
and their statuses are listed in [API errors](./index.md#errors).

## Endpoints the client doesn't cover

For anything newer than your SDK version, call the API directly through the
client, which keeps its credentials and error handling. Paths without
`/api/` are taken as relative to `/api/v1`:

```python
client.request("GET", "/machines")
```

```ts
await client.request('GET', '/machines');
```
