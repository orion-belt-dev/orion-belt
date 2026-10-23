# Risk signals

Every access request records the signals a reviewer, or a future risk
policy, needs to judge it. They are returned as `risk_signals` on
`GET /api/v1/access-requests/{id}` and the list endpoints. `osh requests get`,
`oadmin requests list` and the console's Requests page show them too.

| Signal | Source |
|---|---|
| `source_ip` | The client address the gateway saw. Forwarded headers are used only from `server.trusted_proxies`. |
| `device` | The client's `User-Agent`. `osh`, `ocp` and `oadmin` send `<binary>/<version>`. The value is truncated to 200 characters. |
| `auth_method` | How the request was authenticated (API key, session, JWT). |
| `mfa_enrolled` | Whether the requester has TOTP or a WebAuthn key enrolled. |
| `hour_utc`, `weekday` | When the request was made, in UTC. |
| `target_sensitivity` | The target machine's `sensitivity` tag. |
| `collected_at` | When the signals were captured. |

Requests made before signal collection existed have no `risk_signals`.

## Target sensitivity

Tag machines with `sensitivity` set to `low`, `medium` or `high`. A machine
tagged `pci: in-scope` without a `sensitivity` tag counts as `high`. Anything
else is `unset`.

```sh
oadmin machines update db-01 --tag env=prod --tag sensitivity=high
```

`--tag` on update replaces the whole tag set, so repeat the existing tags.
