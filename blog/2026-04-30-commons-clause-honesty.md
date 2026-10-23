---
slug: commons-clause-honesty
title: Apache 2.0 + Commons Clause — what Orion Belt’s license means
description: Clear explanation of Orion Belt’s Apache 2.0 + Commons Clause license for self-hosted PAM — what you can do, what you cannot sell, and why we disclose it on HN and Reddit.
authors: [orion]
tags: [self-hosted, pam]
keywords: [Commons Clause, Apache 2.0 PAM, open source license self-hosted, Orion Belt license]
---

Security infrastructure requires trust. Burying a restrictive clause until after launch is how open-source communities learn to ignore you. Orion Belt uses **Apache License 2.0 with the Commons Clause**, and we say so in the README, website, and launch posts.

{/* truncate */}

This article is the plain-language license FAQ for people evaluating Orion Belt as **self-hosted privileged access** software.

## What you can do

Under Apache 2.0 + Commons Clause (as we ship it):

- Use Orion Belt internally, including for commercial companies  
- Modify the code  
- Self-host for your team / customers’ *infrastructure you operate as part of your own business* (you are not selling Orion as the product)  
- Read and audit the source  

## What you cannot do

The Commons Clause withholds the right to **Sell** the software. In practice for Orion Belt:

- You may not sell Orion Belt as a product  
- You may not offer a hosted service whose value derives substantially from Orion Belt as the product you are selling  

If you want to build a managed Orion SaaS as your business, that is exactly what the clause is designed to stop without permission.

## Is it “Open Source”?

**OSI “Open Source”** has a specific meaning. Commons Clause software is often called **source-available**. We do not pretend otherwise on Hacker News, Lobsters, or Reddit.

If your organization requires OSI-approved licenses only, Orion Belt may be disallowed — better to know before you invest evaluation time.

## Why not AGPL or a pure Apache 2.0?

Different projects pick different weapons. Our goals:

1. Keep the code auditable for security buyers  
2. Allow free self-hosting for real users  
3. Prevent a cloud vendor from wrapping the repo as their PAM SaaS without a deal  

Whether you agree with that tradeoff is personal. We would rather be declined honestly than adopted under false premises.

## How we talk about it in public

Launch checklist for maintainers:

- One sentence in Show HN / Reddit  
- Link to LICENSE  
- No “fully open source” claims without qualification  

Full legal text: [LICENSE](https://github.com/orion-belt-dev/orion-belt/blob/master/LICENSE) · [commonsclause.com](https://commonsclause.com/)

## Related

- [Announcing Orion Belt](/blog/announcing-orion-belt)  
- [Open-source PAM landscape](/blog/open-source-pam-landscape)
