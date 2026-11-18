---
sidebar_position: 13
title: Compliance reports
---

# Compliance reports

Three organisation-wide reports are built from data the gateway already
keeps: sessions, connections, access requests, grants and their history.
Admins, operators and auditors export them as CSV, PDF or JSON from the
console (**Audit**), with `oadmin reports export`, or from
`GET /api/v1/reports/{name}/export`.

| Report | Question it answers | Parameters |
| --- | --- | --- |
| `soc2-access` | Who accessed what, when, why, and who approved it | `from`, `to` |
| `iso27001-access-review` | Which grants were in force at a point in time | `as_of` |
| `pci-admin-activity` | Who opened sessions on PCI-in-scope machines | `from`, `to` |

Dates are RFC3339 or `YYYY-MM-DD`. A bare `to` or `as_of` date covers the
whole day. The range defaults to the last 30 days, and `as_of` to now.

```bash
oadmin reports export soc2-access --from 2026-07-01 --to 2026-09-30 -o q3-access.csv
oadmin reports export iso27001-access-review --as-of 2026-09-30 --format pdf -o review.pdf
oadmin reports export pci-admin-activity --from 2026-09-01 --format json
```

---

## soc2-access

One row per session started, connection opened and access request made in
the range, oldest first: actor, target, remote user, time, status, reason
and approver. Each session also says what authorised it:

- `access_request:<id>`: an approved request covered its start.
- `permission:<id>`: a grant was in force ("standing" or "time-boxed").
- `role:<role>`: the user is an admin or operator.
- `unresolved`: nothing on record explains it. Investigate these.

RDP, VNC and kubectl connections (1.4.0) are listed as `rdp_session`,
`vnc_session` and `k8s_session` rows, matched to a request or grant of their
own access type: an ssh+scp (`both`) grant never explains an RDP connection.

---

## iso27001-access-review

Every grant in force at `as_of`: user, machine, access type, remote users
(scopes for k8s), grantor, granted and expiry times, and whether it is
`standing` or `time-boxed`. Grants changed or revoked after `as_of` are still
listed as they were then, with `ended_at` and `end_reason`.

---

## pci-admin-activity

Tag machines that are in PCI scope (`--tag` replaces the whole tag set, so
repeat existing tags):

```bash
oadmin machines update pay-01 --tag env=prod --tag pci=in-scope
```

The report lists every session on a tagged machine in the range, flags
administrative accounts (`root`, `admin`, `administrator`) and says whether a
recording is available. RDP, VNC and kubectl connections to tagged machines
are listed too, with the protocol as the source.

---

## Limits

Each source is read up to 5000 rows. When the oldest row read is still inside
the range, the report says `truncated`. Narrow the range to get a complete
report.
