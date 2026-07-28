# Multi-language SDK Plan

Orion Belt already exposes a stable HTTP contract through [docs/openapi/openapi.yaml](openapi/openapi.yaml). That spec is the source of truth for language clients.

This repository will treat the Go SDK as the reference implementation and add language-specific SDKs for:

- Python
- .NET
- JavaScript / TypeScript

## Design

The SDKs should share the same endpoint model, auth semantics, and response shapes:

- `X-API-Key`
- `X-Session-Token`
- `Authorization: Bearer <JWT>`
- public challenge/login flows
- admin endpoints
- file/session/report APIs

Each language client should be a thin layer over the OpenAPI contract, with small ergonomic helpers for:

- authentication
- request retries/timeouts
- typed request and response models
- file downloads/uploads

## Repository Layout

Proposed layout:

```text
sdk/
  python/
  dotnet/
  js/
  common/
docs/
  MULTI_LANGUAGE_SDK.md
```

The `common` area is for shared generation metadata, not shared runtime code.

## Recommended Tooling

- Python: OpenAPI Generator or a pinned handwritten wrapper around generated models
- .NET: OpenAPI Generator or NSwag
- JavaScript/TypeScript: OpenAPI Generator with a TypeScript fetch or axios client

## Implementation Order

1. Keep [docs/openapi/openapi.yaml](openapi/openapi.yaml) updated.
2. Generate baseline clients from the spec.
3. Add thin language-specific helper layers.
4. Add smoke tests against a local server fixture.
5. Publish each client as its own package.

## Package Names

Suggested package names:

- Python: `orion-belt-sdk`
- .NET: `OrionBelt.SDK`
- JavaScript/TypeScript: `@orion-belt/sdk`

## Current Status

- Go SDK: implemented in [pkg/sdk](../pkg/sdk)
- JavaScript/TypeScript SDK: baseline client scaffold started in [sdk/js](../sdk/js)
- Python/.NET: baseline client scaffolds started in [sdk/python](../sdk/python) and [sdk/dotnet](../sdk/dotnet)

The next step is to expand each language client toward fuller OpenAPI parity and add smoke tests per package.