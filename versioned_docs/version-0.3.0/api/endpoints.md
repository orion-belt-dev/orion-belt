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

## Sessions

| Method | Endpoint        | Description              |
|--------|----------------|--------------------------|
| GET    | /sessions       | List active sessions     |
| GET    | /sessions/`{id}`  | Retrieve session recording|
