# .NET SDK

Early `.NET` client for Orion Belt, package name `OrionBelt.SDK`. It is not
published and does not yet cover the full API; the Python and TypeScript SDKs
do.

The HTTP contract is [docs/openapi/openapi.yaml](../../docs/openapi/openapi.yaml).

## What it covers

- An `HttpClient`-based client with API key, session token and bearer token auth
- The public challenge/login flow
- Read and write calls for machines, sessions, reports and files

Build it from [OrionBelt.SDK.csproj](OrionBelt.SDK.csproj).
