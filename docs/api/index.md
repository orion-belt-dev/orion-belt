---
sidebar_position: 21
title: API Reference
---

# Orion-Belt API Reference

Orion-Belt exposes a REST API for integration with external systems and automation scripts.

- Base URL: `http://<server>:8080/api/v1`
- Authentication: `Authorization: Bearer <key>` with an API key (`osh api-keys create`, or the console's **Security → API keys**) or a JWT from `POST /api/v1/public/login/token`
- Content-Type: `application/json`
- Schema: the server serves its OpenAPI document at `/api/v1/openapi.yaml`

```bash
curl -s -H "Authorization: Bearer $ORION_API_KEY" https://orion.example.com/api/v1/machines
```

---

## Available Resources

- `/api/v1/users`, `/api/v1/admin/users` – list, create, update, delete users
- `/api/v1/machines`, `/api/v1/admin/agents` – machines, connected agents, install scripts, agent commands
- `/api/v1/admin/permissions`, `/api/v1/access-requests` – grants and just-in-time requests
- `/api/v1/sessions` – session history, recordings, live watch
- `/api/v1/audit-logs`, `/api/v1/reports` – audit log and compliance reports

Refer to [`endpoints.md`](endpoints.md) for every endpoint.

---

## Errors

:::info[After 1.4.0]
The `code` field ships in the next release; 1.4.0 returns `error` only.
:::

A failed call answers with a status that matches the kind of failure and a
body of the form:

```json
{"error": "request is not pending", "code": "conflict"}
```

| HTTP | `code` | Meaning |
| --- | --- | --- |
| 400 | `invalid` | Malformed input, or a request that breaks a rule |
| 401 | `unauthenticated` | No valid credentials |
| 403 | `forbidden` | Valid credentials, action not allowed |
| 404 | `not_found` | The thing named does not exist |
| 409 | `conflict` | Clashes with current state |
| 503 | `unavailable` | A dependency is down; retrying may work |
| 500 | `internal` | Anything else |

Branch on `code`, not on the message text. A 500 always says
`internal error`: the cause is logged on the gateway and never sent, so
database errors, hostnames and SQL don't reach clients. Request validation
that fails before reaching a service (a missing field, a bad duration) may
answer 400 with `error` only.
