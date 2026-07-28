# .NET SDK

`.NET` client package for Orion Belt.

Source of truth: [docs/openapi/openapi.yaml](../../docs/openapi/openapi.yaml)

Package name: `OrionBelt.SDK`

## What is included

- typed `HttpClient`-based client with API key, session token, and bearer token auth
- public challenge/login helpers
- core read/write helpers for machines, sessions, reports, and files
- record models for the initial surface

## Status

Baseline client scaffold is implemented.

See the project file in [OrionBelt.SDK.csproj](OrionBelt.SDK.csproj).# .NET SDK

Planned .NET client for Orion Belt.

Source of truth: [docs/openapi/openapi.yaml](../../docs/openapi/openapi.yaml)

Suggested package name: `OrionBelt.SDK`

## Planned Surface

- auth helpers
- API key/session/JWT auth
- users, machines, permissions, access requests
- sessions, audit logs, reports, notifications
- plugins, agents, MFA, WebAuthn, SSH keys
- file browser APIs

## Status

Scaffold only.