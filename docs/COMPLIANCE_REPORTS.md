# Compliance reports

Three organisation-wide reports are built from data the gateway already keeps:
sessions, access requests, grants and their history. Admins, operators and
auditors can export them as CSV, PDF or JSON from the console (Audit page),
with `oadmin reports export`, or from `GET /api/v1/reports/{name}/export`.

| Report | Question it answers | Parameters |
|---|---|---|
| `soc2-access` | Who accessed what, when, why, and who approved it | `from`, `to` |
| `iso27001-access-review` | Which grants were in force at a point in time | `as_of` |
| `pci-admin-activity` | Who opened sessions on PCI-in-scope machines | `from`, `to` |

Dates are RFC3339 or `YYYY-MM-DD`. A bare `to` or `as_of` date covers the whole
day. The range defaults to the last 30 days and `as_of` defaults to now.

```sh
oadmin reports export soc2-access --from 2026-07-01 --to 2026-09-30 -o q3-access.csv
oadmin reports export iso27001-access-review --as-of 2026-09-30 --format pdf -o review.pdf
oadmin reports export pci-admin-activity --from 2026-09-01 --format json
```

## soc2-access

One row per session started and per access request made in the range, oldest
first: actor, target, remote user, timestamp, status, stated reason and
approver. Each session also says what authorised it:

- `access_request:<id>`: an approved request for that user, machine and remote
  user covered the session's start. Its reason and reviewer are reported.
- `permission:<id>`: a grant was in force. The reason reads "standing grant" or
  "time-boxed grant", and the approver is whoever granted it.
- `role:<role>`: the user is an admin or operator, who reach every machine
  without a grant.
- `unresolved`: nothing on record explains the session. Investigate these.

RDP, VNC and kubectl connections are listed beside sessions as `rdp_session`,
`vnc_session` and `k8s_session` rows, one per connection the gateway audited
(`tunnel.open`, `k8s.access.open`). They have no remote user, so they are
matched to a request or grant of their own access type instead: an ssh+scp
(`both`) grant never explains an RDP connection. Access requests of every type
are listed; a k8s request shows its namespace/role scopes as the remote user.

## iso27001-access-review

Every grant in force at `as_of`: user, machine, access type, remote users,
grantor, granted and expiry times, and its kind:

- `standing`: no expiry.
- `time-boxed`: expires at `expires_at`.

Standing grants are listed first, then time-boxed ones by expiry. Grants that
were changed or revoked after `as_of` are still listed as they were at that
moment, with `ended_at` and `end_reason` (`updated` or `revoked`). The gateway
keeps this history in `permission_history` from this release on. A review
dated before the upgrade cannot see grants that were revoked before it.

## pci-admin-activity

Tag machines that are in PCI scope. `--tag` on update replaces the whole tag
set, so repeat the tags the machine already has:

```sh
oadmin machines update pay-01 --tag env=prod --tag pci=in-scope
```

The report lists every session in the range on a tagged machine. Every
session through the gateway is privileged access to the target. The
`privileged` column also flags sessions as an administrative account (`root`,
`admin`, `administrator`). `recording_available` says whether a recording can
be played back for the commands that were run.

RDP, VNC and kubectl connections to tagged machines are listed too, with the
protocol as `source`. They are never recorded, and `privileged` is false
because there is no target account to judge; for Kubernetes, what the user
could do is the grant's scope, in `iso27001-access-review`.

## Limits

Each source is read up to 5000 rows. When the oldest row read is still inside
the range, the JSON envelope carries `truncated: true` and the PDF says so.
Narrow the range to get a complete report.
