# SDKs

Orion Belt has API clients for Go, Python and TypeScript, and an early .NET
client. The HTTP contract they implement is
[docs/openapi/openapi.yaml](openapi/openapi.yaml).

| Language | Location | Package | Coverage |
|----------|----------|---------|----------|
| Go | [pkg/sdk](../pkg/sdk) | `github.com/orion-belt-dev/orion-belt/pkg/sdk` | Reference client; see [GO_SDK.md](GO_SDK.md) |
| Python | [sdk/python](../sdk/python) | `orion-belt-sdk` (PyPI) | Every `/api/v1` route |
| TypeScript | [sdk/js](../sdk/js) | `@orion-belt/sdk` (npm) | Every `/api/v1` route |
| .NET | [sdk/dotnet](../sdk/dotnet) | `OrionBelt.SDK` (unpublished) | Auth, machines, sessions, reports, files |

## Design

The Python and TypeScript clients are written by hand, not generated from the
OpenAPI file, and have no runtime dependencies: the Python client uses the
standard library (Python 3.11+) and the TypeScript client uses `fetch`
(Node 20+ or a browser). Both follow the Go SDK for authentication
(`X-API-Key`, `X-Session-Token` or `Authorization: Bearer`), the challenge
login flow, timeouts and error handling.

## Keeping them complete

[sdk/routes.json](../sdk/routes.json) lists every `/api/v1` route. It is
generated from the Go router, and `pkg/api/routes_manifest_test.go` fails
when the two differ. The Python and TypeScript test suites fail if a route in
it has no SDK method, so a new endpoint has to be added to both SDKs before CI
passes:

```sh
go test ./pkg/api -run TestRoutesManifest -update-routes
```

## Releases

Each SDK is released by tag (`sdk-python-v*`, `sdk-js-v*`) through
`.github/workflows/sdk-release.yml`, which publishes to PyPI and npm with
trusted publishing. See the package READMEs for usage.
