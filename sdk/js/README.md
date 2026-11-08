# @orion-belt/sdk

TypeScript client for the [Orion Belt](https://github.com/orion-belt-dev/orion-belt)
API, for console extensions and web integrations.

- Covers every `/api/v1` endpoint the server exposes. A test checks the client
  against the server's route table, so new endpoints can't be missed.
- No runtime dependencies: built on the platform `fetch`, so it runs in Node 20+,
  Deno, Bun and browsers.
- ES module with type declarations.

## Install

```sh
npm install @orion-belt/sdk
```

## Use

```ts
import { ApiError, OrionBeltClient } from '@orion-belt/sdk';

const client = new OrionBeltClient({ baseUrl: 'https://pam.example.com', apiKey: '...' });

for (const machine of await client.listMachines()) {
  console.log(machine.name, machine.is_active);
}

try {
  await client.getMachine('does-not-exist');
} catch (err) {
  if (err instanceof ApiError) console.log(err.statusCode, err.serverMessage);
}
```

Method names follow the [Go SDK](https://github.com/orion-belt-dev/orion-belt/tree/master/pkg/sdk)
in camelCase. Request and response fields keep the API's snake_case names. For
an endpoint the client has no method for yet, use
`client.request(method, path, body)`.

Options: `apiKey`, `sessionToken`, `bearerToken`, `timeoutMs` (default 30 s, 0
disables it), `userAgent` (ignored by browsers), `headers`, and `fetch` to
supply your own implementation.

### Authentication

Pass a credential in the options, or log in. The login helpers store the
credential they get back on the client (read it back from `client.credentials`).

| Helper | Credential |
|--------|------------|
| `loginWithSshKey(username, signer)` | API key |
| `loginWithSshSession(username, signer)` | session token (+ JWT) |
| `loginWithSshJwt(username, signer)` | JWT bearer token |
| `loginWithPassword(username, password, totpCode)` | session token (+ JWT) |

A `Signer` proves you hold an SSH key registered to the user. It needs a
`publicKey` (the authorized_keys line) and a `sign(data)` that returns the
signature format and the raw signature, sync or async. In Node, for an ed25519 key:

```ts
import { createPrivateKey, createPublicKey, sign } from 'node:crypto';
import { readFileSync } from 'node:fs';

const privateKey = createPrivateKey(readFileSync('id_ed25519.pem')); // PKCS#8 PEM
const signer = {
  publicKey: readFileSync('id_ed25519.pub', 'utf8').trim(),
  sign: (data: Uint8Array) => ({ format: 'ssh-ed25519', signature: sign(null, data, privateKey) }),
};
await client.loginWithSshKey('alice', signer);
```

Node can't read OpenSSH-format private keys. Convert one with
`ssh-keygen -p -m PKCS8 -f id_ed25519` (on a copy). For RSA keys, return
`rsa-sha2-256` and an RSA-SHA256 PKCS#1 v1.5 signature.

`logout()` ends the HTTP session and clears the session and bearer tokens from
the client. The server does not revoke a JWT at logout; it stays valid until
it expires.

### WebSockets

`terminalWsUrl()` and `sessionWatchWsUrl()` return the `ws://`/`wss://` URL.
The server authenticates the upgrade request by header, so use a client that
can send `client.authHeaders()` (for example `ws` in Node). The browser
`WebSocket` API cannot set headers; in a browser the session cookie is used
instead.

## Develop

From `sdk/js`, with Node 22+:

```sh
npm ci --ignore-scripts
npm run lint
npm run check
npm test          # node:test with coverage, fails under 90% lines/branches
npm run build
```

If the server adds an endpoint, regenerate the route list
(`go test ./pkg/api -run TestRoutesManifest -update-routes` from the repo root).
Then add the method and its entry in `test/contract.test.ts`.

## Release

Bump `version` in `package.json` and push a tag `sdk-js-v<version>`. The
`SDK release` workflow tests, builds and publishes to npm with provenance.

## License

Apache License 2.0 with the Commons Clause condition. See [LICENSE](https://github.com/orion-belt-dev/orion-belt/blob/master/LICENSE).
