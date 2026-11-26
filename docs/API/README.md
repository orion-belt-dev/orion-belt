# Orion Belt HTTP API

Canonical OpenAPI 3.0 specification:

- **Source:** [../openapi/openapi.yaml](../openapi/openapi.yaml)
- **Live (running server):** `GET /api/v1/openapi.yaml`
- **Postman:** [postman_collection.json](postman_collection.json) · [postman_env.json](postman_env.json)

## Browse with Swagger UI

```bash
# Against a running gateway
docker run --rm -p 8090:8080 \
  -e SWAGGER_JSON_URL=http://host.docker.internal:8080/api/v1/openapi.yaml \
  swaggerapi/swagger-ui
# open http://localhost:8090
```

Or locally without Docker:

```bash
npx @redocly/cli preview-docs docs/openapi/openapi.yaml
# or
npx swagger-ui-watcher docs/openapi/openapi.yaml
```

## Auth quick reference

| Mechanism | Header / cookie / query |
|-----------|-------------------------|
| API key | `X-API-Key: <key>` |
| Session | `X-Session-Token` or cookie `session_token` or `?token=` |
| JWT | `Authorization: Bearer <jwt>` |

Admin routes under `/api/v1/admin/*` require role **admin** or **operator**.

## Usage analytics endpoint

The console dashboard uses `GET /api/v1/dashboard/usage` for a live operational snapshot.

Example:

```bash
curl -sS -H "X-Session-Token: $TOKEN" \
  "http://localhost:8080/api/v1/dashboard/usage?window_hours=24&top=5"
```

Response fields include:

- `access_volume` (sessions and request counts)
- `approval_latency` (sample size, avg, p50, p95 in seconds)
- `top_targets` (most-accessed machines in the selected window)

## Related docs

- [API_STABILITY.md](../API_STABILITY.md) — what we promise for `/api/v1`
- [V1_RELEASE_CRITERIA.md](../V1_RELEASE_CRITERIA.md) — what v1.0 covers
- [RELEASE_SMOKE.md](../RELEASE_SMOKE.md) — `make release-smoke`
- [SSH_CA.md](../SSH_CA.md) — SSH CA
- [SRS-UI.md](../SRS-UI.md) — web console
- [ARCHITECTURE.md](../ARCHITECTURE.md)
- [PACKAGING.md](../PACKAGING.md)
