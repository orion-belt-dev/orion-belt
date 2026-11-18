---
slug: orion-belt-v1-1-and-v1-2
title: Orion Belt 1.1 and 1.2 — tracing, report exports, usage analytics and a one-line installer
description: Orion Belt 1.1.0 adds OpenTelemetry tracing across the gateway and agent, PDF/CSV/SIEM report exports, a usage analytics dashboard, an org-wide notification policy and a Go SDK. 1.2.0 adds a one-line server installer and in-app notifications.
date: 2026-08-05T12:00:00Z
authors: [orion]
tags: [pam, ops, observability]
keywords: [Orion Belt 1.1, Orion Belt 1.2, SSH OpenTelemetry, PAM report export, SSH usage analytics, PAM Go SDK, SSH gateway installer]
---

Two releases landed two weeks after 1.0: **Orion Belt 1.1.0** on July 30
and **1.2.0** on July 31. 1.1 is about seeing what the gateway does. 1.2 is
about getting it installed.

{/* truncate */}

## 1.1.0: observability and reporting

### OpenTelemetry tracing

The gateway and agents can emit OTLP traces to any collector (OpenTelemetry
Collector, Jaeger, Tempo, Honeycomb). One trace covers an SSH session end to
end: authorization at the gateway, the gateway → agent hop, and the agent →
target hop. When a connection is slow, the trace shows which hop. It's off by
default and costs nothing when disabled. Details in
[Tracing SSH sessions with OpenTelemetry](/blog/opentelemetry-ssh-tracing).

### Report exports

Audit and security reports export as **PDF** for reviewers, **CSV** for
spreadsheets, and a **SIEM-oriented** format for log pipelines, from the
console or the API.

### Usage analytics

A dashboard with access volume, approval latency and the most used targets.
It answers "who actually uses production access, and how long do approvals
take?" without exporting the audit log first.

### Notification policy

Users choose which notifications they get; admins set an org-wide policy that
bounds those choices. A user can't opt out of something the policy requires.

### Go SDK

A Go client for the public API, for scripts and integrations that would
otherwise hand-roll HTTP calls. First-pass Python, JavaScript and .NET
clients ship alongside it as a starting point.

## 1.2.0: install and day-to-day

### One-line server installer

`scripts/install-server.sh` installs the server from the distribution
packages, sets up a local PostgreSQL (or reuses one, or takes a DSN), asks for
the public URL and WebAuthn settings, enables the systemd or OpenRC service,
and writes an `osh` client config for the user who ran it.

The public URL is now a first-class setting (`server.public_url`). The
**Add agent** flow and the setup wizard use it, so generated agent install
commands point at the right address.

### In-app notifications

Access request events appear in the console's notification bell. Admins also
get a notice when WebAuthn is enabled in the config but fails to start, which
used to fail silently.

### Fixes

- Agents and users with recorded sessions or grants can be hard-deleted; the
  schema repairs its foreign keys on migrate.
- Deleting an agent through the API also removes its synthetic agent user,
  as the CLI already did.

## Known limitations in 1.2.0

From the release notes, so you can plan around them:

- Revoking or pausing an agent can be undone when the agent reconnects.
- `POST /public/register/client` still accepts `is_admin` for lab bootstrap;
  lock it down in production.
- Recording retention targets cast files by modification time and may leave
  encrypted files or session rows behind.

## Upgrade

Install 1.2.0 and restart. Migrations run on start.

Release notes:
[v1.1.0](https://github.com/orion-belt-dev/orion-belt/releases/tag/v1.1.0) ·
[v1.2.0](https://github.com/orion-belt-dev/orion-belt/releases/tag/v1.2.0)
