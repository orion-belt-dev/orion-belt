---
slug: temporary-kubernetes-access-rolebindings
title: How to give engineers temporary Kubernetes access with expiring RoleBindings
description: A step-by-step guide to just-in-time Kubernetes access — why standing kubeconfigs are a risk, how expiring RoleBindings and impersonation work, and how Orion Belt 1.4 issues, reconciles and removes them through an in-cluster agent with no exposed API server.
authors: [orion]
tags: [kubernetes, pam, security]
keywords: [temporary Kubernetes access, just-in-time Kubernetes access, Kubernetes RoleBinding expiry, kubectl access approval, Kubernetes PAM, Kubernetes impersonation, zero standing privilege Kubernetes]
---

**Temporary Kubernetes access means a user gets a RoleBinding for a specific
namespace and role, for a fixed time, and the binding is deleted when that
time is up.** Kubernetes RBAC has no expiry field, so something outside the
cluster has to create and remove those bindings reliably. This guide shows
the pattern and how Orion Belt 1.4 does it.

{/* truncate */}

## Why standing kubeconfigs are a problem

The usual setup: every engineer has a kubeconfig with a long-lived
credential bound to a broad role, often `cluster-admin` "temporarily".

- **Kubeconfigs get copied**: into CI variables, laptops, chat messages.
- **Bindings outlive their reason.** A role granted for an incident stays.
- **Revoking is manual**, and depends on someone remembering.
- **The API server is reachable** from wherever those laptops are.

[Kubernetes' own RBAC good practices](https://kubernetes.io/docs/concepts/security/rbac-good-practices/)
recommend least privilege and minimal use of cluster-admin. Expiring access
is how you get there without slowing people down.

## The pattern

1. A user requests a role in a namespace (`payments/edit`) with a reason and
   a duration.
2. Someone approves.
3. A controller creates a RoleBinding for that user, labelled with its
   expiry.
4. kubectl traffic reaches the API server as that user, so RBAC enforces
   exactly the binding.
5. When the time is up, the binding is deleted, even if the system that
   granted it is down.

Steps 4 and 5 are where home-grown versions usually fall short.

## How Orion Belt does it

**Grants** have access type `k8s` and scopes of the form `namespace/role`.
`payments/edit` binds the `edit` ClusterRole in `payments` as a RoleBinding;
`*/view` binds `view` cluster-wide as a ClusterRoleBinding.

**Subjects** are Kubernetes users named `orion:<username>`, so a binding
issued by Orion Belt can never match an identity from another authenticator.

**An agent runs inside the cluster** and dials out to the gateway, like the
agents on hosts. The API server is never exposed.

**kubectl goes through `osh kube`.** The agent forwards requests to the API
server with its service account and `Impersonate-User: orion:<user>`,
after stripping any credentials or impersonation headers the client sent.
RBAC then allows exactly what the issued bindings allow
([user impersonation](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#user-impersonation)).

**Reconciling** keeps the cluster matched to the grants. The gateway
compares the bindings it manages (named `orion-*`, labelled
`app.kubernetes.io/managed-by=orion-belt`) with active grants and creates,
updates or deletes to match: when a grant changes, when the agent connects,
and every 30 seconds.

**The agent enforces expiry on its own.** It deletes expired bindings every
minute, even while the gateway is unreachable.

## Step by step

**1. Install the in-cluster agent.** Register a machine for the cluster in
the console (its name is the cluster's name in grants), then:

```bash
kubectl create namespace orion-belt
kubectl -n orion-belt create secret generic orion-belt-agent-key \
  --from-file=agent_key=./agent-key
kubectl apply -f orion-belt-agent.yaml   # from deploy/kubernetes/, with server and agent name set
```

The manifest's ClusterRole lets the agent manage bindings, `bind`
ClusterRoles and `impersonate` users. Narrow `bind` with `resourceNames` to
the roles you allow (for example `view`, `edit`).

**2. Request access.**

```bash
osh kube prod-eu -r --scope payments/edit --reason "INC-42" -d 1800
# Kubernetes API of prod-eu relayed on 127.0.0.1:53817
# export KUBECONFIG=/home/alice/.orion-belt/kube/prod-eu.yaml
```

**3. Use kubectl normally.**

```bash
kubectl get pods             # namespace defaults to the first scope's
kubectl -n default get pods  # Forbidden: outside the grant
```

The generated kubeconfig holds no credentials. It points at a loopback relay,
and the gateway authenticates the SSH connection behind it.

**4. Check for drift.**

```bash
oadmin machines kube-reconcile prod-eu          # report only
oadmin machines kube-reconcile prod-eu --apply  # fix now
```

## Audit trail

Every step is logged: the request and approval (`access_type: k8s` with its
scopes), each binding created, updated or deleted with the reason
(`granted`, `expired`, `revoked`, `orphaned`), and each kubectl relay
connection. The [compliance reports](/docs/guides/compliance-reports) list
them as `k8s_session` rows.

## FAQ

**Can a Kubernetes RoleBinding expire on its own?**
No. RBAC objects have no expiry. A controller has to delete them; Orion Belt
does it from both the gateway and the in-cluster agent.

**Does this work with managed clusters (EKS, GKE, AKS)?**
The agent only uses standard Kubernetes RBAC and impersonation through its
own service account, nothing provider-specific, and needs outbound access to
the gateway. Our end-to-end tests run on kind; tell us how it goes on a
managed cluster.

**Does the user need a kubeconfig from the cloud provider?**
No. `osh kube` writes one that points at the local relay.

## Related

- [Orion Belt v1.4](/blog/orion-belt-v1-4)
- [Kubernetes guide](/docs/guides/kubernetes)
- [What is privileged access management?](/blog/what-is-privileged-access-management)
