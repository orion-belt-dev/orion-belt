# Deployment hardening

Work through this checklist before exposing a gateway on a production
network, and again before tagging a release (see
[V1_RELEASE_CRITERIA.md](V1_RELEASE_CRITERIA.md)). It complements
[SETUP.md](SETUP.md), [REVERSE_PROXY.md](REVERSE_PROXY.md) and
[OBSERVABILITY.md](OBSERVABILITY.md).

## Network

- [ ] Terminate TLS for the console and API in a reverse proxy
      ([REVERSE_PROXY.md](REVERSE_PROXY.md)).
- [ ] Block port 8080 from everywhere except the proxy. The API binds to the
      same address as SSH, so a direct connection would bypass TLS.
- [ ] Restrict who can reach the gateway's SSH port (2222) with a VPN,
      zero-trust access or firewall rules.
- [ ] Keep PostgreSQL off public networks: use a private network, TLS, or a
      managed database with an address allowlist.
- [ ] Do not open inbound ports on target hosts. Agents connect out to the
      gateway.
- [ ] Do not publish `/metrics`. It is unauthenticated; scrape it on the
      internal network.

## Client addresses

- [ ] Behind a proxy, set `server.trusted_proxies` to the proxy's address and
      nothing broader. Without it, all clients share one rate-limit
      allowance and the audit log records the proxy's address.
- [ ] Confirm the proxy replaces `X-Forwarded-For` instead of appending the
      client's value, and that a forged header does not change the address in
      the gateway's logs ([verification steps](REVERSE_PROXY.md#verifying-the-setup)).

## TLS and browser sign-in

- [ ] Use a certificate from a trusted CA on the proxy.
- [ ] Set the WebAuthn `rp_id` and `origins` to the public hostname.
- [ ] Keep session lifetimes short. Enable `auth.mfa_required`, and allow
      password login only if you need it.

## Accounts

- [ ] Create the first admin with `orion-belt-server setup` rather than the
      unauthenticated first-run API call, so the window never exists on the
      network.
- [ ] Keep admin and operator accounts few. Operators can manage users and
      grants but cannot create or change admins.
- [ ] Enrol admins in WebAuthn or TOTP before go-live.
- [ ] Prefer SSH-key challenge login for the CLI. Reserve password plus TOTP
      for browser and break-glass access.
- [ ] Review **Permissions > All grants** regularly and remove grants that are
      no longer needed.

## Secrets and configuration

- [ ] Make `server.yaml` mode `0600`, owned by the service user. Never commit
      real secrets.
- [ ] Keep the recording encryption key, database password, JWT secret and
      webhook URLs in a secret store or a restricted `EnvironmentFile`.
- [ ] Rotate webhooks and API keys when people leave. Revoke unused keys under
      **Security > API keys**.
- [ ] Protect `ssh_ca.master_key` and the CA private keys as the most
      sensitive material in the deployment. Anyone holding them can sign
      certificates for any user or host.
- [ ] Treat agent install scripts as secrets. Each contains the agent's
      private key.

## Recording

- [ ] Enable `recording.enabled` and set `retention_days` to your policy.
- [ ] Set `recording.encryption_key` unless plaintext recordings on disk are
      acceptable. `compression: gzip` is fine either way.
- [ ] Back up the recording volume separately from the database, and test
      playback after a restore.
- [ ] Monitor free space on the recording volume. SSH sessions currently
      continue if a recording cannot be started.

## Runtime

- [ ] Run the gateway as an unprivileged user. Agents need root to start
      sessions as other local users.
- [ ] Keep the hardening in the packaged service units (`ProtectSystem=` and
      related settings), or apply equivalent restrictions.
- [ ] Scrape `/metrics` and ship the JSON logs. The alerts in
      [OBSERVABILITY.md](OBSERVABILITY.md) are a starting point.
- [ ] Apply package updates promptly, and run `make cve` in CI.

## Drills

- [ ] Offboard a user: revoke their grants, API keys and SSH certificates,
      then delete the account.
- [ ] Disconnect an agent and reinstall it from a fresh install script.
- [ ] Confirm the audit log shows sign-in, grant, approval and rejection, and
      session start and end.
