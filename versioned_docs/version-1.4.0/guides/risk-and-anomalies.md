---
sidebar_position: 14
title: Risk, explain and anomalies
---

# Risk signals, access explain and anomaly flags

---

## Why can (or can't) I reach a machine?

```bash
osh explain root@web-01               # for yourself
osh explain root@web-01 --for alice   # admins and auditors, for anyone
```

The answer walks the relationships the gateway checked: role, grants, their
access type, remote users and expiry. A denied SSH session prints the same
explanation.

---

## Risk signals

Every access request records what a reviewer needs to judge it, shown in
`osh requests get`, `oadmin requests list` and the console's **Requests**
page:

| Signal | Source |
| --- | --- |
| `source_ip` | The client address the gateway saw (forwarded headers only from `server.trusted_proxies`) |
| `device` | The client's `User-Agent`, e.g. `osh/1.4.0` |
| `auth_method` | API key, session or JWT |
| `mfa_enrolled` | Whether the requester has TOTP or WebAuthn |
| `hour_utc`, `weekday` | When the request was made |
| `target_sensitivity` | The machine's `sensitivity` tag: `low`, `medium` or `high` (untagged PCI-in-scope counts as `high`) |

```bash
oadmin machines update db-01 --tag env=prod --tag sensitivity=high
```

---

## Risk policy

An admin can route new requests by their signals. Without a policy, every
request waits for review.

- **Escalate**: any matching condition makes the request approvable only by
  an admin.
- **Auto-approve**: when every condition holds, the request is approved at
  once and audited as `access.auto_approve`. `max_sensitivity` (`low` or
  `medium`) and `max_duration_seconds` (at most 24 h) are required;
  high-sensitivity targets and unlimited durations are never auto-approved.

```bash
oadmin risk-policy show
oadmin risk-policy set --file - <<'JSON'
{
  "auto_approve": {"enabled": true, "max_sensitivity": "low", "max_duration_seconds": 3600,
                   "hours_utc": {"start": 8, "end": 18}, "source_cidrs": ["10.0.0.0/8"],
                   "require_mfa": true, "remote_users": ["deploy"]},
  "escalate": {"enabled": true, "sensitivities": ["high"], "without_mfa": true,
               "outside_hours_utc": {"start": 7, "end": 20}}
}
JSON
```

Hour ranges are UTC, start included and end excluded, and may wrap midnight.

---

## Flagged sessions

Unusual sessions are flagged, never blocked. Each flag names the signal that
raised it.

| Kind | Raised when |
| --- | --- |
| `first_time_access` | A user's first session on a `high`-sensitivity machine |
| `off_hours` | A session starts outside the UTC hours the user usually works, once they have 10 earlier sessions |

Review them under **Audit → Flagged sessions**, with
`oadmin anomalies list [--for alice]`, or `GET /api/v1/anomalies`.
