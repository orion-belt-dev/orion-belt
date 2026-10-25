---
sidebar_position: 21
title: API Reference
---

# Orion-Belt API Reference

Orion-Belt exposes a REST API for integration with external systems and automation scripts.

- Base URL: `http://<server>:8080/api/v1`
- Authentication: API tokens generated in the server
- Content-Type: `application/json`

---

## Available Resources

- `/agents` – List, register, update, delete agents
- `/users` – Create, update, delete users
- `/permissions` – Grant, revoke, list permissions
- `/sessions` – Active session monitoring and recording retrieval

Refer to [`endpoints.md`](endpoints.md) for detailed endpoint documentation.

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
