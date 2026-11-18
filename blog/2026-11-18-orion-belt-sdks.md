---
slug: orion-belt-sdks
title: Automating Orion Belt from Python and TypeScript
description: Orion Belt 1.5.0 ships Python and TypeScript SDKs that cover every API endpoint, with no runtime dependencies. How they stay complete, how to log in with an SSH key, and a JIT access script to start from.
date: 2026-11-18T11:00:00Z
authors: [orion]
tags: [pam, ops]
keywords: [PAM API, Python PAM SDK, TypeScript PAM SDK, JIT access automation, SSH access API, Orion Belt SDK]
---

Most of what people build around a PAM gateway is glue: a chat bot that
files access requests, a nightly job that reviews standing grants, a deploy
pipeline that asks for an hour on production. With **1.5.0**, that glue can
be written against a typed client in Python or TypeScript instead of
hand-rolled HTTP calls.

{/* truncate */}

```sh
pip install orion-belt-sdk        # Python 3.11+
npm install @orion-belt/sdk       # Node 20+, Deno, Bun, browsers
```

## Complete by construction

An SDK that covers "the main endpoints" quietly falls behind. These two cover
every `/api/v1` route, and they stay that way because the build says so.

The server's routes are generated from the Go router into
[`sdk/routes.json`](https://github.com/orion-belt-dev/orion-belt/blob/master/sdk/routes.json).
A server test fails if the router and the file disagree, and each SDK has a
contract test that fails if a route has no method. Adding an endpoint without
adding it to both clients turns CI red.

Both clients follow the Go SDK's method names (`list_machines` in Python,
`listMachines` in TypeScript), so code ports between the three with little
thought.

## No dependencies

The Python client uses only the standard library. The TypeScript client uses
the platform `fetch`. Neither pulls anything into your dependency tree, which
matters for a tool that holds admin credentials to your access gateway.

## Log in with an SSH key

Scripts don't need a stored password. Prove you hold a key registered to the
user, and the client keeps the API key it gets back:

```python
from cryptography.hazmat.primitives import serialization
from orion_belt_sdk import OrionBeltClient

key = serialization.load_ssh_private_key(open("id_ed25519", "rb").read(), password=None)


class Signer:
    public_key = key.public_key().public_bytes(
        serialization.Encoding.OpenSSH, serialization.PublicFormat.OpenSSH
    ).decode()

    def sign(self, data: bytes) -> tuple[str, bytes]:
        return "ssh-ed25519", key.sign(data)


client = OrionBeltClient("https://orion.example.com")
client.login_with_ssh_key("deploy-bot", Signer())
```

`cryptography` is only needed for this example; the SDK itself doesn't
require it. Password and TOTP login, session tokens and JWTs work too. See
[SDKs](/docs/api/sdks#authentication).

## Example: ask for an hour on a machine

A deploy job that requests just-in-time access, then waits for a human to
approve it:

```ts
import { OrionBeltClient } from '@orion-belt/sdk';

const client = new OrionBeltClient({
  baseUrl: 'https://orion.example.com',
  apiKey: process.env.ORION_API_KEY,
});

const machine = await client.getMachineByName('web-01');

const request = await client.createAccessRequest({
  machine_id: machine.id,
  reason: `deploy ${process.env.GIT_SHA}`,
  duration: 3600,
  remote_users: ['deploy'],
});

console.log(`Access request ${request.id} is waiting for approval`);
```

The approver sees the request, its reason and its
[risk signals](/docs/guides/risk-and-anomalies) in the console or in chat,
and the access expires on its own an hour later.

## License

The SDKs are AGPL v3.0, like the rest of Orion Belt. If you bundle one into
an application you distribute or serve to others, check that your license is
compatible. [More on the license change](/blog/orion-belt-agpl).

## Related

- [SDK reference](/docs/api/sdks)
- [JIT access approvals](/blog/jit-access-approvals)
- [Orion Belt v1.5](/blog/orion-belt-v1-5)
