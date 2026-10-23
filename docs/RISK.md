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

## Flagged sessions

Sessions are flagged when they look unusual, and are never blocked for it.
Each flag names the signal that raised it, so a reviewer can act on it.

| Kind | Raised when |
|---|---|
| `first_time_access` | A user opens their first session on a machine whose sensitivity is `high`. That includes untagged PCI-in-scope machines. |
| `off_hours` | A session starts outside every UTC hour, give or take one hour, in which the user started a session before. It is only judged once the user has 10 earlier sessions. |

Both SSH sessions through the gateway and console terminals are checked when
they start. Flags are written to the audit log as `anomaly.<kind>` and listed
separately from denials:

- Console: **Audit → Flagged sessions** (admins, operators, auditors)
- CLI: `oadmin anomalies list [--for alice] [--limit 50]`
- API: `GET /api/v1/anomalies?user_id=&limit=`
