---
slug: orion-belt-agpl
title: Orion Belt is now open source under the AGPL v3.0
description: From 1.5.0, Orion Belt is licensed under the GNU AGPL v3.0 only, replacing Apache 2.0 with the Commons Clause. What you can do, what you must share, and what it means for the SDKs.
date: 2026-11-18T13:00:00Z
authors: [orion]
tags: [self-hosted, pam]
keywords: [AGPL PAM, open source PAM, AGPL SSH bastion, Orion Belt license, Commons Clause to AGPL]
---

From **1.5.0**, Orion Belt is licensed under the
[GNU Affero General Public License v3.0 only](https://github.com/orion-belt-dev/orion-belt/blob/master/LICENSE)
(`AGPL-3.0-only`). It replaces Apache 2.0 with the Commons Clause, which we
used from the first release. Orion Belt is now open source in the
[OSI sense](https://opensource.org/license/agpl-v3), not only
source-available.

{/* truncate */}

## What you can do

- Run Orion Belt for any purpose, including inside a company and for paying
  customers' infrastructure.
- Read, audit and modify the code.
- Package it, mirror it and redistribute it.

There is no fee and no "internal use only" condition. The Commons Clause
restriction on selling Orion Belt is gone.

## What you must do in return

The AGPL asks for one thing: if you pass the software on, you pass on the
source too.

- **Distributing it** (a modified binary, an appliance, an image you hand to
  others): ship the corresponding source under the AGPL.
- **Offering a modified version over a network** (users connect to your
  changed gateway): offer those users the source of your version.

Running an unmodified Orion Belt triggers neither. Your configuration, your
scripts and the machines behind the gateway are not covered by the license.
This is a summary, not legal advice; the LICENSE text is what applies.

## Why we changed

When we [explained the Commons Clause](/blog/commons-clause-honesty), we
named three goals: keep the code auditable, keep self-hosting free, and stop
a vendor from wrapping the project as a closed hosted service. The AGPL meets
all three. Anyone can run a hosted Orion Belt, but they have to publish the
changes they make to it, which is the protection we were after.

What the Commons Clause added on top was a ban on selling, and that came at a
cost. It is not an OSI license, so teams with an "OSI-approved only" policy
could not adopt Orion Belt at all, and packagers and security reviewers had to
read a custom clause before they could even evaluate it. The AGPL is a license
those teams already have a policy for.

## The SDKs

The Python and TypeScript SDKs are AGPL too. An application that bundles an
SDK and is distributed, or served to others over a network, should be under an
AGPL-compatible license. Calling the REST API directly, with any HTTP client,
does not bring the AGPL into your code.

The SDK packages on PyPI and npm still carry the old license metadata from
their 1.0.0 release. Their next release picks up the AGPL.

## Older releases

Releases up to and including 1.4.x stay under the license they shipped with.
A license doesn't change retroactively. Upgrade to 1.5.0 to run under the
AGPL.

## Related

- [Orion Belt v1.5](/blog/orion-belt-v1-5)
- [Apache 2.0 + Commons Clause, before 1.5.0](/blog/commons-clause-honesty)
- [Open-source PAM landscape](/blog/open-source-pam-landscape)
