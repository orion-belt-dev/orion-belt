# SDKs

Language clients for the Orion Belt API. The Go SDK in [pkg/sdk](../pkg/sdk) is
the reference for auth and request behaviour; the HTTP contract is
[docs/openapi/openapi.yaml](../docs/openapi/openapi.yaml).

| Directory | Package | Status |
|-----------|---------|--------|
| [python](python) | `orion-belt-sdk` (PyPI) | Every `/api/v1` route |
| [js](js) | `@orion-belt/sdk` (npm) | Every `/api/v1` route |
| [dotnet](dotnet) | `OrionBelt.SDK` | Scaffold |

[routes.json](routes.json) lists the server's `/api/v1` routes. It is generated
from the Go router, and `pkg/api/routes_manifest_test.go` fails when the two
differ. The Python and TypeScript contract tests fail when a route in it has no
SDK method. After adding an endpoint:

```sh
go test ./pkg/api -run TestRoutesManifest -update-routes
```

Then add the method to both SDKs. Releases are cut by tag (`sdk-python-v*`,
`sdk-js-v*`); see each package's README.
