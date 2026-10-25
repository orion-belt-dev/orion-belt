---
sidebar_position: 22
title: API Endpoints
---

# API Endpoints

## Agents

| Method | Endpoint      | Description            |
|--------|---------------|------------------------|
| GET    | /agents       | List all agents        |
| POST   | /agents       | Register a new agent   |
| GET    | /agents/`{id}`  | Retrieve agent details |
| DELETE | /agents/`{id}`  | Delete an agent        |
| POST   | /api/v1/admin/agents/`{machine_id}`/command | Send `orion:status`, `orion:reload`, `orion:drain`, `orion:undrain` or `orion:update` (see [Agent remote management](../guides/agent-management)) |

## Users

| Method | Endpoint      | Description           |
|--------|---------------|-----------------------|
| GET    | /users        | List all users        |
| POST   | /users        | Create a new user     |
| GET    | /users/`{id}`   | Get user details      |
| DELETE | /users/`{id}`   | Delete user           |

## Permissions

| Method | Endpoint          | Description         |
|--------|------------------|-------------------|
| GET    | /permissions     | List all permissions|
| POST   | /permissions     | Grant permission    |
| DELETE | /permissions/`{id}`| Revoke permission   |
| POST   | /api/v1/admin/permissions/bulk | Grant one access type to every user × machine pair (up to 500; after 1.4.0) |
| POST   | /api/v1/admin/permissions/bulk-revoke | Revoke several grants (up to 500; after 1.4.0) |
| GET    | /api/v1/admin/permissions/effective | Effective access for a user or a machine (after 1.4.0) |

## Sessions

| Method | Endpoint        | Description              |
|--------|----------------|--------------------------|
| GET    | /sessions       | List active sessions     |
| GET    | /sessions/`{id}`  | Retrieve session recording|
