---
sidebar_position: 21
title: API Reference
---

# Orion-Belt API Reference

Orion-Belt exposes a REST API for integration with external systems and automation scripts.

- Base URL: `http://<server>:8080/api`
- Authentication: API tokens generated in the server
- Content-Type: `application/json`

---

## Available Resources

- `/agents` – List, register, update, delete agents
- `/users` – Create, update, delete users
- `/permissions` – Grant, revoke, list permissions
- `/sessions` – Active session monitoring and recording retrieval

Refer to [`endpoints.md`](endpoints.md) for detailed endpoint documentation.
