import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiPage } from "../lib/api";
import { isTunnel, targetField } from "../lib/access";
import type { BulkResult, EffectiveAccess, Machine, Permission, User } from "../lib/types";
import { fmtTime, shortId } from "../lib/format";
import { useToast } from "../components/Toast";
import { Badge } from "../components/Badge";
import { MultiPicker } from "../components/MultiPicker";
import { Pagination } from "../components/DataTable";

type View = "grants" | "effective";

const PAGE_SIZE = 50;

// useDebounced returns value once it has stopped changing for ms.
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

function splitList(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}

function bulkSummary(r: BulkResult, verb: string, n: number | undefined): string {
  const failed = r.failed?.length ?? 0;
  return failed ? `${verb} ${n ?? 0}; ${failed} could not be applied` : `${verb} ${n ?? 0}`;
}

export function PermissionsPage() {
  const usersQ = useQuery({ queryKey: ["users"], queryFn: () => api<User[]>("/users") });
  const machinesQ = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });
  const users = useMemo(() => usersQ.data ?? [], [usersQ.data]);
  const machines = useMemo(() => machinesQ.data ?? [], [machinesQ.data]);
  const [view, setView] = useState<View>("grants");

  const userName = useMemo(() => {
    const map = new Map(users.map((u) => [u.id, u.username]));
    return (id: string) => map.get(id) || shortId(id);
  }, [users]);
  const machineName = useMemo(() => {
    const map = new Map(machines.map((m) => [m.id, m.name]));
    return (id: string) => map.get(id) || shortId(id);
  }, [machines]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Permissions</h1>
          <p>Who can reach which machine, and as whom. Grant to several users and machines at once, or see what someone can actually do.</p>
        </div>
      </div>
      <GrantForm users={users} machines={machines} />
      <div className="card">
        <div className="seg" role="tablist" aria-label="Permissions view">
          <button type="button" role="tab" aria-selected={view === "grants"} onClick={() => setView("grants")}>
            Grants
          </button>
          <button type="button" role="tab" aria-selected={view === "effective"} onClick={() => setView("effective")}>
            Effective access
          </button>
        </div>
        {view === "grants" ? (
          <GrantsTable users={users} machines={machines} userName={userName} machineName={machineName} />
        ) : (
          <EffectiveView users={users} machines={machines} />
        )}
      </div>
    </>
  );
}

function GrantForm({ users, machines }: { users: User[]; machines: Machine[] }) {
  const { toast } = useToast();
  const qc = useQueryClient();
  const [userIds, setUserIds] = useState<string[]>([]);
  const [machineIds, setMachineIds] = useState<string[]>([]);
  const [accessType, setAccessType] = useState("both");
  const [remoteUsers, setRemoteUsers] = useState("root");
  const [durationHours, setDurationHours] = useState(0);
  const pairs = userIds.length * machineIds.length;

  const grant = useMutation({
    mutationFn: async () => {
      const common: Record<string, unknown> = {
        access_type: accessType,
        remote_users: isTunnel(accessType) ? [] : splitList(remoteUsers),
      };
      if (durationHours > 0) common.duration_seconds = Math.round(durationHours * 3600);
      if (pairs === 1) {
        await api("/admin/permissions", {
          method: "POST",
          body: JSON.stringify({ ...common, user_id: userIds[0], machine_id: machineIds[0] }),
        });
        return "Permission granted";
      }
      const r = await api<BulkResult>("/admin/permissions/bulk", {
        method: "POST",
        body: JSON.stringify({ ...common, user_ids: userIds, machine_ids: machineIds }),
      });
      return bulkSummary(r, "Granted", r.granted);
    },
    onSuccess: (msg) => {
      toast(msg);
      void qc.invalidateQueries({ queryKey: ["permissions"] });
      void qc.invalidateQueries({ queryKey: ["effective"] });
    },
    onError: (e: Error) => toast(e.message, "err"),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pairs === 0) {
      toast("Choose at least one user and one machine", "err");
      return;
    }
    if (pairs > 500) {
      toast(`${pairs} grants selected; at most 500 at a time`, "err");
      return;
    }
    grant.mutate();
  }

  return (
    <form className="card" onSubmit={onSubmit} style={{ marginBottom: "1rem" }}>
      <h2 className="card-title">Grant access</h2>
      <div className="form-grid">
        <MultiPicker
          legend="Users"
          items={users}
          getId={(u) => u.id}
          getLabel={(u) => u.username}
          selected={userIds}
          onChange={setUserIds}
        />
        <MultiPicker
          legend="Machines"
          items={machines}
          getId={(m) => m.id}
          getLabel={(m) => m.name}
          selected={machineIds}
          onChange={setMachineIds}
        />
        <div>
          <label className="field" htmlFor="grant-access-type">
            Access type
          </label>
          <select id="grant-access-type" value={accessType} onChange={(e) => setAccessType(e.target.value)}>
            <option value="ssh">ssh</option>
            <option value="scp">scp</option>
            <option value="both">both (ssh + scp)</option>
            <option value="rdp">rdp</option>
            <option value="vnc">vnc</option>
            <option value="k8s">k8s</option>
          </select>
          <label className="field" htmlFor="grant-remote-users" style={{ marginTop: "0.75rem" }}>
            {targetField(accessType, true).label}
          </label>
          <input
            id="grant-remote-users"
            value={isTunnel(accessType) ? "" : remoteUsers}
            onChange={(e) => setRemoteUsers(e.target.value)}
            disabled={isTunnel(accessType)}
            placeholder={targetField(accessType, true).placeholder}
          />
          <label className="field" htmlFor="grant-ttl" style={{ marginTop: "0.75rem" }}>
            TTL hours (0 = no expiry)
          </label>
          <input
            id="grant-ttl"
            type="number"
            min={0}
            value={durationHours}
            onChange={(e) => setDurationHours(Number(e.target.value) || 0)}
          />
        </div>
      </div>
      <div className="row" style={{ marginTop: "0.75rem" }}>
        <button className="btn sm" type="submit" disabled={grant.isPending || pairs === 0}>
          {pairs > 1 ? `Grant ${pairs} permissions` : "Grant"}
        </button>
        {pairs > 1 ? (
          <span className="muted">
            {userIds.length} user{userIds.length === 1 ? "" : "s"} × {machineIds.length} machine
            {machineIds.length === 1 ? "" : "s"}
          </span>
        ) : null}
      </div>
    </form>
  );
}

function GrantsTable({
  users,
  machines,
  userName,
  machineName,
}: {
  users: User[];
  machines: Machine[];
  userName: (id: string) => string;
  machineName: (id: string) => string;
}) {
  const { toast } = useToast();
  const qc = useQueryClient();
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  // Every filter change starts again from the first page, and drops the
  // selection, so a bulk revoke never reaches rows that are no longer shown.
  const [query, setQueryRaw] = useState("");
  const [accessType, setAccessTypeRaw] = useState("");
  const [userId, setUserIdRaw] = useState("");
  const [machineId, setMachineIdRaw] = useState("");
  const filterSetter = (set: (v: string) => void) => (v: string) => {
    set(v);
    setPage(0);
    setSelected(new Set());
  };
  const setQuery = filterSetter(setQueryRaw);
  const setAccessType = filterSetter(setAccessTypeRaw);
  const setUserId = filterSetter(setUserIdRaw);
  const setMachineId = filterSetter(setMachineIdRaw);
  const q = useDebounced(query, 300);

  // Filtering and paging happen on the server, so large grant sets stay fast.
  const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(page * PAGE_SIZE) });
  if (q.trim()) params.set("q", q.trim());
  if (accessType) params.set("access_type", accessType);
  if (userId) params.set("user_id", userId);
  if (machineId) params.set("machine_id", machineId);
  const permsQ = useQuery({
    queryKey: ["permissions", params.toString()],
    queryFn: () => apiPage<Permission>(`/admin/permissions?${params.toString()}`),
    placeholderData: (prev) => prev,
  });
  const rows = permsQ.data?.rows || [];
  const total = permsQ.data?.total ?? 0;

  const revoke = useMutation({
    mutationFn: (ids: string[]) =>
      api<BulkResult>("/admin/permissions/bulk-revoke", { method: "POST", body: JSON.stringify({ ids }) }),
    onSuccess: (r) => {
      toast(bulkSummary(r, "Revoked", r.revoked));
      setSelected(new Set());
      void qc.invalidateQueries({ queryKey: ["permissions"] });
      void qc.invalidateQueries({ queryKey: ["effective"] });
    },
    onError: (e: Error) => toast(e.message, "err"),
  });

  function revokeSelected() {
    const ids = [...selected];
    if (ids.length && confirm(`Revoke ${ids.length} permission${ids.length === 1 ? "" : "s"}?`)) revoke.mutate(ids);
  }

  async function editGrant(p: Permission) {
    const remotes = prompt("Remote users or scopes (comma-separated)", (p.remote_users || []).join(", "));
    if (remotes === null) return;
    const hours = prompt("TTL hours from now (blank = keep, 0 = permanent)", "");
    if (hours === null) return;
    const body: Record<string, unknown> = { remote_users: splitList(remotes) };
    if (hours.trim() !== "") {
      const h = Number(hours);
      body.duration_seconds = h > 0 ? Math.round(h * 3600) : 0;
    }
    try {
      await api(`/admin/permissions/${encodeURIComponent(p.id)}`, { method: "PATCH", body: JSON.stringify(body) });
      toast("Permission updated");
      void qc.invalidateQueries({ queryKey: ["permissions"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  const pageIds = rows.map((p) => p.id);
  const allOnPage = pageIds.length > 0 && pageIds.every((id) => selected.has(id));
  function togglePage() {
    const next = new Set(selected);
    for (const id of pageIds) {
      if (allOnPage) next.delete(id);
      else next.add(id);
    }
    setSelected(next);
  }
  function toggle(id: string) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  }

  return (
    <div role="tabpanel">
      <div className="form-grid" style={{ marginBottom: "0.75rem" }}>
        <div>
          <label className="field" htmlFor="grants-search">
            Search
          </label>
          <input
            id="grants-search"
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="User, machine, account or scope"
          />
        </div>
        <div>
          <label className="field" htmlFor="grants-type">
            Access type
          </label>
          <select id="grants-type" value={accessType} onChange={(e) => setAccessType(e.target.value)}>
            <option value="">Any</option>
            {["ssh", "scp", "both", "rdp", "vnc", "k8s"].map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="field" htmlFor="grants-user">
            User
          </label>
          <select id="grants-user" value={userId} onChange={(e) => setUserId(e.target.value)}>
            <option value="">Any</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.username}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="field" htmlFor="grants-machine">
            Machine
          </label>
          <select id="grants-machine" value={machineId} onChange={(e) => setMachineId(e.target.value)}>
            <option value="">Any</option>
            {machines.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="row" style={{ marginBottom: "0.5rem" }}>
        <button
          type="button"
          className="btn danger sm"
          onClick={revokeSelected}
          disabled={selected.size === 0 || revoke.isPending}
        >
          Revoke selected ({selected.size})
        </button>
        {selected.size ? (
          <button type="button" className="btn secondary sm" onClick={() => setSelected(new Set())}>
            Clear selection
          </button>
        ) : null}
        <span className="muted" aria-live="polite">
          {permsQ.isFetching ? "Searching…" : `${total} grant${total === 1 ? "" : "s"}`}
        </span>
      </div>
      {permsQ.isError ? (
        <div className="empty">Could not load grants: {(permsQ.error as Error).message}</div>
      ) : permsQ.isLoading ? (
        <p className="muted">Loading…</p>
      ) : rows.length === 0 ? (
        <div className="empty">No grants match. Change the filters, or grant access above.</div>
      ) : (
        <>
          <div className="table-scroll" tabIndex={0} role="region" aria-label="Grants">
          <table>
            <thead>
              <tr>
                <th className="check-col">
                  <input type="checkbox" checked={allOnPage} onChange={togglePage} aria-label="Select all on this page" />
                </th>
                <th>User</th>
                <th>Machine</th>
                <th>Access</th>
                <th>Remote users / scopes</th>
                <th>Granted</th>
                <th>Expires</th>
                <th>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => (
                <tr key={p.id}>
                  <td className="check-col">
                    <input
                      type="checkbox"
                      checked={selected.has(p.id)}
                      onChange={() => toggle(p.id)}
                      aria-label={`Select ${userName(p.user_id)} on ${machineName(p.machine_id)}`}
                    />
                  </td>
                  <td>{userName(p.user_id)}</td>
                  <td>{machineName(p.machine_id)}</td>
                  <td>
                    <Badge status={p.access_type}>{p.access_type}</Badge>
                  </td>
                  <td className="mono">{(p.remote_users || []).join(", ") || "—"}</td>
                  <td className="mono">{fmtTime(p.granted_at)}</td>
                  <td className="mono">{fmtTime(p.expires_at)}</td>
                  <td>
                    <div className="row">
                      <button type="button" className="btn secondary sm" onClick={() => void editGrant(p)}>
                        Edit
                      </button>
                      <button
                        type="button"
                        className="btn danger sm"
                        onClick={() => {
                          if (confirm(`Revoke ${userName(p.user_id)}'s access to ${machineName(p.machine_id)}?`))
                            revoke.mutate([p.id]);
                        }}
                      >
                        Revoke
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
          <Pagination
            page={page}
            pageCount={Math.max(1, Math.ceil(total / PAGE_SIZE))}
            total={total}
            pageSize={PAGE_SIZE}
            onPage={setPage}
          />
        </>
      )}
    </div>
  );
}

function EffectiveView({ users, machines }: { users: User[]; machines: Machine[] }) {
  const [by, setBy] = useState<"user" | "machine">("user");
  const [id, setId] = useState("");
  const q = useQuery({
    queryKey: ["effective", by, id],
    queryFn: () => api<EffectiveAccess[]>(`/admin/permissions/effective?${by}_id=${encodeURIComponent(id)}`),
    enabled: !!id,
  });
  const rows = q.data || [];

  return (
    <div role="tabpanel">
      <p className="muted" style={{ marginTop: 0 }}>
        What someone can actually do once roles and grants are resolved: admins and operators reach every machine, and a
        “both” grant is ssh and scp.
      </p>
      <div className="form-grid" style={{ marginBottom: "0.75rem" }}>
        <div>
          <label className="field" htmlFor="effective-by">
            Show access for
          </label>
          <select
            id="effective-by"
            value={by}
            onChange={(e) => {
              setBy(e.target.value as "user" | "machine");
              setId("");
            }}
          >
            <option value="user">A user, across machines</option>
            <option value="machine">A machine, across users</option>
          </select>
        </div>
        <div>
          <label className="field" htmlFor="effective-id">
            {by === "user" ? "User" : "Machine"}
          </label>
          <select id="effective-id" value={id} onChange={(e) => setId(e.target.value)}>
            <option value="">Select…</option>
            {(by === "user" ? users : machines).map((x) => (
              <option key={x.id} value={x.id}>
                {"username" in x ? x.username : x.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      {!id ? (
        <div className="empty">Choose a {by} to resolve their access.</div>
      ) : q.isError ? (
        <div className="empty">Could not resolve access: {(q.error as Error).message}</div>
      ) : q.isLoading ? (
        <p className="muted">Resolving…</p>
      ) : rows.length === 0 ? (
        <div className="empty">No access. Grant some above, or approve an access request.</div>
      ) : (
        <div className="table-scroll" tabIndex={0} role="region" aria-label="Effective access">
        <table>
          <thead>
            <tr>
              <th>{by === "user" ? "Machine" : "User"}</th>
              <th>Access</th>
              <th>As</th>
              <th>Via</th>
              <th>Expires</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={`${r.source}-${r.machine_id}-${r.user_id}-${r.access_type}-${i}`}>
                <td>{by === "user" ? r.machine_name : r.username}</td>
                <td>
                  {r.access_type === "*" ? "every type" : <Badge status={r.access_type}>{r.access_type}</Badge>}
                </td>
                <td className="mono">
                  {r.access_type === "*" ? "any account" : (r.remote_users || []).join(", ") || "—"}
                </td>
                <td className="mono">{r.source.startsWith("role:") ? r.source.replace("role:", "role ") : `grant ${shortId(r.source.replace("permission:", ""))}`}</td>
                <td className="mono">{r.access_type === "*" ? "—" : fmtTime(r.expires_at) || "never"}</td>
              </tr>
            ))}
          </tbody>
        </table>
        </div>
      )}
    </div>
  );
}
