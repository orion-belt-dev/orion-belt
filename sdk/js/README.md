# JavaScript / TypeScript SDK

TypeScript client package for Orion Belt.

Source of truth: [docs/openapi/openapi.yaml](../../docs/openapi/openapi.yaml)

Package name: `@orion-belt/sdk`

## What is included

- typed `fetch` client with API key, session token, and bearer token auth
- public auth helpers for challenge and login flows
- common read/write helpers for machines, sessions, reports, and files
- typed request/response models for the initial surface

## Status

Baseline client scaffold is implemented.

See the package entry point in [src/index.ts](src/index.ts).