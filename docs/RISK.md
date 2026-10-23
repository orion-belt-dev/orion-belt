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

## Risk policy

An admin can route new access requests by their signals. Without a policy,
every request waits for the usual review.

- **Escalate**: any matching condition makes the request approvable only by
  an admin. An operator's approval is refused.
- **Auto-approve**: when every condition holds, the request is approved at
  once. No reviewer is recorded (`reviewed_by` is null and reports name the
  approver as "risk policy"), and the approval is audited as
  `access.auto_approve`. `max_sensitivity` (`low` or `medium`) and
  `max_duration_seconds` (at most 24 h) are required. High-sensitivity
  targets and unlimited durations are never auto-approved.

Escalation is checked first. Each request keeps the outcome and its reasons
as `risk_decision`, and the console's Requests page shows it.

```sh
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

Hour ranges are UTC, with the start included and the end excluded. They can
wrap midnight, for example `{"start": 22, "end": 6}`. Only admins can change
the policy, and each change is audited as `risk_policy.update`.

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
