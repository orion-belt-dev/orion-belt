# Python SDK

Python client package for Orion Belt.

Source of truth: [docs/openapi/openapi.yaml](../../docs/openapi/openapi.yaml)

Package name: `orion-belt-sdk`

## What is included

- typed client with API key, session token, and bearer token auth
- public challenge/login helpers
- core read/write helpers for machines, sessions, reports, and files
- dataclass response models for the initial surface

## Status

Baseline client scaffold is implemented.

See the package entry point in [src/orion_belt_sdk/__init__.py](src/orion_belt_sdk/__init__.py).