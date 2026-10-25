---
sidebar_position: 12
title: Kubernetes
---

# Kubernetes

:::info[New in 1.4.0]
Needs the gateway, agents and `osh` on 1.4.0 or later.
:::

Kubernetes access follows the same request, approve and expire flow as SSH.
An approved `k8s` grant becomes a RoleBinding (or ClusterRoleBinding) for the
user, and the binding is deleted when the grant ends. Nobody holds a standing
kubeconfig, and the API server is never exposed: an agent inside the cluster
dials out to the gateway, like host agents do.

---

## How it works

- **Grants** have access type `k8s`. Their scopes are `namespace/role`:
  `payments/edit` binds the `edit` ClusterRole in `payments` (a RoleBinding),
  and `*/view` binds `view` across the cluster (a ClusterRoleBinding). One
  grant can carry several scopes.
- **Subjects** are Kubernetes users named `orion:<gateway username>`, so a
  binding issued by Orion Belt never matches an identity from another
  authenticator.
- **Reconciling** keeps the cluster in line with the grants. The gateway
  compares the bindings it manages (named `orion-*`, labelled
  `app.kubernetes.io/managed-by=orion-belt`) with the active grants, then
  creates, updates or deletes to match. It runs when a grant changes, when
  the cluster's agent connects, and every 30 seconds. The desired state lives
  in the gateway's database, so a gateway restart loses nothing.
- **The agent enforces expiry too.** Each binding carries its expiry, and the
  agent deletes expired bindings every minute on its own, even while the
  gateway is unreachable.
- **kubectl** goes through `osh kube`. The agent forwards requests to the API
  server with its service account and `Impersonate-User: orion:<user>`, after
  dropping any credentials or impersonation headers the client sent. RBAC
  then allows exactly what the issued bindings allow, including `exec`.

---

## 1. Install the in-cluster agent

1. Register a machine for the cluster (**Add agent** in the console). Its
   name is the cluster's name in grants and in `osh kube <name>`. Keep the
   private key.
2. Create the namespace and the key secret:

   ```bash
   kubectl create namespace orion-belt
   kubectl -n orion-belt create secret generic orion-belt-agent-key \
     --from-file=agent_key=./agent-key
   ```

3. Download
   [`deploy/kubernetes/orion-belt-agent.yaml`](https://github.com/orion-belt-dev/orion-belt/blob/master/deploy/kubernetes/orion-belt-agent.yaml),
   set `ORION_SERVER_HOST` and `ORION_AGENT_NAME`, and apply it:

   ```bash
   kubectl apply -f orion-belt-agent.yaml
   ```

The manifest's ClusterRole is everything the agent can do: manage
RoleBindings and ClusterRoleBindings, `bind` ClusterRoles, and `impersonate`
users. Narrow `bind` with `resourceNames` to the roles you allow in grants
(for example `view`, `edit`, `admin`). The agent itself refuses to touch any
binding not named `orion-*`, and to bind any subject that is not an `orion:`
user.

---

## 2. Request and use access

```bash
osh kube prod-eu -r --scope payments/edit --reason "INC-42" -d 1800
# Kubernetes API of prod-eu relayed on 127.0.0.1:53817
# export KUBECONFIG=/home/alice/.orion-belt/kube/prod-eu.yaml

kubectl get pods            # the namespace defaults to the first scope's
kubectl -n default get pods # Forbidden: outside the grant
```

`osh kube` writes a kubeconfig that points at a local port. It holds no
credentials: the relay listens on loopback, and the gateway authenticates the
SSH connection behind it. The relay stays open until Ctrl-C.

Admins grant directly in the console (**Permissions**, access type `k8s`,
scopes in the scopes field) or with `oadmin`:

```bash
oadmin permissions grant --user alice --machine prod-eu --access-type k8s \
  --remote-user payments/edit --remote-user '*/view' --duration 3600
```

---

## 3. Check and repair

```bash
oadmin machines kube-reconcile prod-eu          # report drift only
oadmin machines kube-reconcile prod-eu --apply  # fix it now
```

A dry run lists the bindings that would be created, updated or deleted.
Anything listed for deletion is expired, revoked or orphaned.

---

## 4. Network interruptions

:::info[After 1.4.0]
Ships in the next release; not in 1.4.0.
:::

`osh kube` behaves like `osh rdp` when the network drops: the local relay
stays up, the gateway connection is redialled with backoff, and the next
`kubectl` command goes through once it is back. A `kubectl` command in flight,
such as a `logs -f` or `exec`, ends and can be rerun. Bindings are unaffected:
they follow grants on the gateway and the agent, not the client's connection.
See [RDP and VNC](./rdp-vnc#4-network-interruptions) for the keepalive, limit
and timeout details shared by all relays.

---

## 5. Audit

| Action | When |
| --- | --- |
| `access.request`, `access.approve` | As for SSH, with `access_type: k8s` and the scopes |
| `k8s.binding.create` / `update` / `delete` | A binding was issued, extended or removed, with the reason: `granted`, `grant changed`, `expired`, `revoked` or `orphaned` |
| `k8s.binding.*_failed` | The agent or API server refused the change |
| `k8s.access.open` / `close` / `denied` | A kubectl relay connection |
| `k8s.reconcile` | An admin applied a reconcile |

kubectl connections appear in the [compliance reports](./compliance-reports)
as `k8s_session` rows, and k8s grants in the access review.
