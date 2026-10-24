# Kubernetes

Kubernetes access follows the same request, approve and expire flow as SSH.
An approved `k8s` grant becomes a RoleBinding (or ClusterRoleBinding) for the
user, and the binding is deleted when the grant ends. Nobody gets a standing
kubeconfig, and the API server is never exposed: an agent inside the cluster
dials out to the gateway, as host agents do.

## How it works

- **Grants** have access type `k8s`. Their `remote_users` are scopes,
  `namespace/role`: `payments/edit` binds the `edit` ClusterRole in the
  `payments` namespace (a RoleBinding), and `*/view` binds `view` across the
  cluster (a ClusterRoleBinding). One grant can carry several scopes.
- **Subjects** are Kubernetes users named `orion:<gateway username>`, so a
  binding issued here never matches an identity from another authenticator.
- **Reconciling** keeps the cluster in line with the grants. The gateway
  compares the bindings it manages (labelled
  `app.kubernetes.io/managed-by=orion-belt`, named `orion-*`) with the active
  `k8s` grants. It creates what is missing, updates what changed and deletes
  the rest: expired, revoked, or orphaned. It runs when a grant changes, when
  the cluster's agent connects, and every 30 seconds. The desired state lives
  in the database, so a gateway restart loses nothing: when the agent
  reconnects, whatever expired in the meantime is removed.
- **The agent enforces expiry too.** Each binding carries its expiry in the
  `orion-belt.dev/expires-at` annotation, and the agent deletes expired
  bindings every minute on its own, so access ends on time even while the
  gateway is unreachable.
- **kubectl** goes through `osh kube`. The agent forwards each request to the
  API server with its service account and `Impersonate-User:
  orion:<user>`, after dropping any `Authorization` or `Impersonate-*` header
  the client sent. Kubernetes RBAC then allows exactly what the issued
  bindings allow. Upgraded requests pass through too: `kubectl exec` is
  tested against a real cluster, and `attach` and `port-forward` take the
  same path.

## Install the in-cluster agent

1. Register a machine for the cluster on the gateway. Its name is the
   cluster's name in `osh kube <name>` and in grants. Use Add agent in the
   console (or `POST /api/v1/admin/agents/install-script`) and keep the
   private key.
2. Create the namespace and the key secret:

   ```sh
   kubectl create namespace orion-belt
   kubectl -n orion-belt create secret generic orion-belt-agent-key \
     --from-file=agent_key=./agent-key
   ```

3. Edit `ORION_SERVER_HOST` and `ORION_AGENT_NAME` in
   [`deploy/kubernetes/orion-belt-agent.yaml`](../deploy/kubernetes/orion-belt-agent.yaml),
   then apply it:

   ```sh
   kubectl apply -f deploy/kubernetes/orion-belt-agent.yaml
   ```

The manifest's ClusterRole is everything the agent can do: manage
RoleBindings and ClusterRoleBindings, `bind` ClusterRoles, and `impersonate`
users. `bind` is what lets a grant give out a role the agent does not hold.
Restrict it with `resourceNames` to the roles you allow in grants (for
example `view`, `edit`, `admin`). The agent itself refuses to touch any
binding not named `orion-*`, and to bind any subject that is not an `orion:`
user.

Outside a cluster (for testing), set the connection in the agent config
instead of using the pod's service account:

```yaml
agent:
  name: prod-eu
  kubernetes:
    enabled: true
    api_server: https://10.0.0.10:6443
    token_file: /etc/orion-belt/k8s-token
    ca_file: /etc/orion-belt/k8s-ca.crt
```

## Grant and use access

Ask for access and wait for approval, then open the relay:

```sh
osh kube prod-eu -r --scope payments/edit --reason "INC-42" -d 1800
# Kubernetes API of prod-eu relayed on 127.0.0.1:53817
# export KUBECONFIG=/home/alice/.orion-belt/kube/prod-eu.yaml
kubectl get pods            # namespace defaults to the first scope's
kubectl -n default get pods # Forbidden: outside the grant
```

`osh kube` writes a kubeconfig that points at a local port. It holds no
credentials: the relay listens on loopback, and the gateway authenticates the
SSH connection behind it. The relay stays open until Ctrl-C.

Admins grant directly from the console (Permissions, access type `k8s`, scopes
in the remote users field), from `oadmin`, or the API:

```sh
oadmin permissions grant --user alice --machine prod-eu --access-type k8s \
  --remote-user payments/edit --remote-user '*/view' --duration 3600
```

## Check and repair

```sh
oadmin machines kube-reconcile prod-eu          # report drift only
oadmin machines kube-reconcile prod-eu --apply  # fix it now
```

A dry run lists the bindings that would be created, updated or deleted.
Anything listed for deletion is expired, revoked or orphaned. The same report
is `GET /api/v1/admin/machines/{id}/kubernetes/reconcile`, and `POST` applies
it.

## Audit

| Action | When |
|---|---|
| `access.request`, `access.approve` | As for SSH, with `access_type: k8s` and the scopes in `remote_users` |
| `k8s.binding.create` / `update` / `delete` | A binding was issued, extended or removed. `reason` is `granted`, `grant changed`, `expired`, `revoked` or `orphaned`. Attributed to the user the binding is for. |
| `k8s.binding.*_failed` | The agent or API server refused the change, with `error` |
| `k8s.access.open` / `close` / `denied` | A kubectl relay connection, with bytes and duration on close |
| `k8s.reconcile` | An admin applied a reconcile |

The compliance reports include kubectl connections (`k8s_session` rows) and
k8s grants; see [COMPLIANCE_REPORTS.md](COMPLIANCE_REPORTS.md).

## Testing against kind

`e2e/kubernetes` runs the agent side against a real API server. With a kind
cluster:

```sh
kind create cluster --name orion-e2e
kubectl create namespace orion-belt && kubectl create namespace payments
# apply the ServiceAccount and RBAC objects from deploy/kubernetes/orion-belt-agent.yaml
mkdir -p /tmp/kind-e2e
kubectl -n orion-belt create token orion-belt-agent > /tmp/kind-e2e/token
kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d > /tmp/kind-e2e/ca.crt
kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' > /tmp/kind-e2e/server
ORION_KIND_E2E=/tmp/kind-e2e go test ./e2e/kubernetes
```
