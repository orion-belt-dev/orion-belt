# Running behind a reverse proxy

The gateway serves the web console and HTTP API on port 8080 without TLS. In
production, put a reverse proxy in front of it to terminate TLS. This page
covers the gateway settings the proxy depends on and the templates shipped in
[`deploy/reverse-proxy/`](../deploy/reverse-proxy/).

SSH on port 2222 is a separate TCP service and does not go through an HTTP
proxy. Publish it directly, or through a firewall or load balancer.

## What the gateway expects from a proxy

| Requirement | Why |
|---|---|
| Forward the client address in `X-Forwarded-For` or `X-Real-IP`, replacing any value the client sent | Per-IP rate limits and audit-log addresses use it. |
| List the proxy in `server.trusted_proxies` | Forwarding headers are ignored from any peer not on this list. |
| Pass the `Host` header unchanged, including a non-default port | WebSocket connections are accepted only when `Origin` matches `Host`. |
| Proxy WebSocket upgrades, with a long read timeout | The web terminal and live session watch use WebSockets. |
| Allow request bodies of at least your largest file upload | The web file browser uploads each file in a single request. |

## Trusted proxies

By default the gateway trusts no proxy. The client address is the TCP peer,
and `X-Forwarded-For` and `X-Real-IP` are ignored. This is deliberate: if the
gateway trusted those headers from anyone, a client could choose its own
address, get a fresh rate-limit allowance on every request, and write a
false IP into the audit log.

Behind a proxy, every request then appears to come from the proxy. All
clients share a single rate-limit allowance, and the audit log records the
proxy's address. To fix this, list the proxy's address:

```yaml
server:
  public_url: "https://orion.example.com"
  trusted_proxies:
    - "127.0.0.1"        # proxy on the same host
    # - "10.0.4.12"      # or a dedicated proxy host
    # - "10.0.4.0/24"    # or the subnet a pool of proxies runs in
```

Entries are IP addresses or CIDR ranges. List only addresses that belong to
your proxies. Any host in a trusted range can set its own client address.

For the container image, set `ORION_TRUSTED_PROXIES` to a comma-separated
list instead, for example `ORION_TRUSTED_PROXIES=10.0.4.12,10.0.5.0/24`.

An invalid entry is logged at startup, and the gateway falls back to trusting
no proxy.

## nginx

[`deploy/reverse-proxy/nginx.conf`](../deploy/reverse-proxy/nginx.conf) is a
complete server block for nginx 1.25.1 or later.

1. Copy it to `/etc/nginx/conf.d/orion-belt.conf`.
2. Replace `orion.example.com` and the certificate paths.
3. Set `trusted_proxies: ["127.0.0.1"]` in `server.yaml`, or the address nginx
   connects from if it runs on another host.
4. Run `nginx -t`, reload nginx, and restart the gateway.

The template sets `X-Forwarded-For` to `$remote_addr` instead of appending to
it with `$proxy_add_x_forwarded_for`. With a single proxy, this discards
anything the client put in the header.

## Caddy

[`deploy/reverse-proxy/Caddyfile`](../deploy/reverse-proxy/Caddyfile) obtains
certificates automatically. It reads the hostname and upstream from
`ORION_PUBLIC_HOST` and `ORION_UPSTREAM`:

```bash
export ORION_PUBLIC_HOST=orion.example.com
export ORION_UPSTREAM=127.0.0.1:8080
caddy run --config deploy/reverse-proxy/Caddyfile
```

Caddy passes `Host` through, handles WebSocket upgrades and replaces
client-supplied `X-Forwarded-For` by default, so the template needs no extra
header settings.

## Docker Compose

[`docker-compose.proxy.yml`](../docker-compose.proxy.yml) adds Caddy to the
production stack:

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.proxy.yml \
  --env-file .env.prod up -d
```

The overlay:

- publishes 80 and 443 on Caddy and stops publishing the gateway's 8080, so
  the API is reachable only through the proxy;
- keeps SSH on 2222 published directly;
- pins the compose network to `172.30.0.0/24`, gives Caddy the fixed address
  `172.30.0.10`, and trusts only that address.

If a stack from `docker-compose.prod.yml` is already running, run
`docker compose down` once before applying the overlay, so the network can be
recreated with the fixed subnet. Volumes are not affected. If `172.30.0.0/24`
is already in use on your network, change the subnet and Caddy's address in
the overlay.

The overlay needs Docker Compose 2.24.4 or later.

## Firewall

`server.host` is the bind address for both SSH and the HTTP API, so the API
listens on the same interfaces as SSH. When the proxy is the only intended
route to the API, block port 8080 from everywhere except the proxy.
Otherwise, clients can bypass TLS by connecting to the gateway directly.

## Metrics

`/metrics` has no authentication. Both templates block it at the proxy.
Prometheus should scrape the gateway on port 8080 over the internal network.

## Verifying the setup

1. **Console:** open `https://orion.example.com/ui` and sign in.
2. **WebSockets:** open a web terminal. If it fails to connect, the proxy is
   not forwarding the upgrade, or it is rewriting `Host`.
3. **Client address:** sign in, then check the latest `auth.login` entry
   under **Audit**. It should show your address, not the proxy's.
4. **Header spoofing:** send an unauthenticated request with a forged header:

   ```bash
   curl -s -o /dev/null -H 'X-Forwarded-For: 203.0.113.9' \
     https://orion.example.com/api/v1/auth/me
   ```

   The gateway logs `Authentication failed for GET /api/v1/auth/me from
   <address>`. The address must be yours, not `203.0.113.9`.
