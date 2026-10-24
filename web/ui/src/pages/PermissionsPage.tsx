import { useMemo, useState } from "react";
import type { FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { isTunnel, targetField } from "../lib/access";
import type { Machine, Permission, User } from "../lib/types";
import { fmtTime, shortId } from "../lib/format";
import { useToast } from "../components/Toast";
import { Badge } from "../components/Badge";
import { Pagination, SortTh, TableToolbar, useTableState } from "../components/DataTable";

type ViewMode = "user" | "machine" | "all";

export function PermissionsPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const table = useTableState<Permission>({ pageSize: 25 });
  const usersQ = useQuery({ queryKey: ["users"], queryFn: () => api<User[]>("/users") });
  const machinesQ = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });
  const [userId, setUserId] = useState("");
  const [machineId, setMachineId] = useState("");
  const [accessType, setAccessType] = useState("both");
  const [remoteUsers, setRemoteUsers] = useState("root");
  const [durationHours, setDurationHours] = useState(0);

  const [viewMode, setViewMode] = useState<ViewMode>("all");
  const [viewUserId, setViewUserId] = useState("");
  const [viewMachineId, setViewMachineId] = useState("");
  const viewId = viewMode === "user" ? viewUserId : viewMode === "machine" ? viewMachineId : "all";

  const permsQ = useQuery({
    queryKey: ["permissions", viewMode, viewId],
    queryFn: () => {
      if (viewMode === "all") return api<Permission[]>("/admin/permissions");
      return api<Permission[]>(`/permissions/${viewMode}/${encodeURIComponent(viewId)}`);
    },
    enabled: viewMode === "all" || !!viewId,
  });

  const grant = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = {
        user_id: userId,
        machine_id: machineId,
        access_type: accessType,
        remote_users: isTunnel(accessType)
          ? []
          : remoteUsers
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
      };
      if (durationHours > 0) body.duration_seconds = Math.round(durationHours * 3600);
      return api("/admin/permissions", { method: "POST", body: JSON.stringify(body) });
    },
    onSuccess: () => {
      toast("Permission granted");
      void qc.invalidateQueries({ queryKey: ["permissions"] });
    },
    onError: (e: Error) => toast(e.message, "err"),
  });

  const userName = useMemo(() => {
    const map = new Map((usersQ.data || []).map((u) => [u.id, u.username]));
    return (id: string) => map.get(id) || shortId(id);
  }, [usersQ.data]);
  const machineName = useMemo(() => {
    const map = new Map((machinesQ.data || []).map((m) => [m.id, m.name]));
    return (id: string) => map.get(id) || shortId(id);
  }, [machinesQ.data]);

  function onGrant(e: FormEvent) {
    e.preventDefault();
    if (!userId || !machineId) {
      toast("Select user and machine", "err");
      return;
    }
    grant.mutate();
  }

  async function revoke(id: string, label: string) {
    if (!confirm(`Revoke access to ${label}?`)) return;
    try {
      await api(`/admin/permissions/${encodeURIComponent(id)}`, { method: "DELETE" });
      toast("Permission revoked");
      void qc.invalidateQueries({ queryKey: ["permissions"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function editGrant(p: Permission) {
    const remotes = prompt("Remote users (comma-separated)", (p.remote_users || []).join(", "));
    if (remotes === null) return;
    const hours = prompt("TTL hours from now (blank = keep, 0 = permanent)", "");
    if (hours === null) return;
    const body: Record<string, unknown> = {
      remote_users: remotes
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
    };
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

  const perms = permsQ.data || [];
  const processed = useMemo(() => {
    return table.process(
      perms,
      (p, key) => {
        if (key === "peer")
          return viewMode === "machine" ? userName(p.user_id) : machineName(p.machine_id);
        if (key === "access") return p.access_type;
        if (key === "granted") return p.granted_at || "";
        if (key === "expires") return p.expires_at || "";
        return "";
      },
      (p) =>
        `${machineName(p.machine_id)} ${userName(p.user_id)} ${p.access_type} ${(p.remote_users || []).join(" ")}`,
    );
  }, [perms, viewMode, machineName, userName, table.query, table.sortKey, table.sortDir, table.page, table.pageSize]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Permissions</h1>
          <p>Who can reach which machine, and as whom.</p>
        </div>
      </div>
      <form className="card" onSubmit={onGrant} style={{ marginBottom: "1rem" }}>
        <h3>Grant access</h3>
        <div className="form-grid">
          <div>
            <label className="field">User</label>
            <select value={userId} onChange={(e) => setUserId(e.target.value)} required>
              <option value="">Select…</option>
              {(usersQ.data || []).map((u) => (
                <option key={u.id} value={u.id}>
                  {u.username}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="field">Machine</label>
            <select value={machineId} onChange={(e) => setMachineId(e.target.value)} required>
              <option value="">Select…</option>
              {(machinesQ.data || []).map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="field">Access type</label>
            <select value={accessType} onChange={(e) => setAccessType(e.target.value)}>
              <option value="ssh">ssh</option>
              <option value="scp">scp</option>
              <option value="both">both (ssh + scp)</option>
              <option value="rdp">rdp</option>
              <option value="vnc">vnc</option>
              <option value="k8s">k8s</option>
            </select>
          </div>
          <div>
            <label className="field" htmlFor="grant-remote-users">
              {targetField(accessType, true).label}
            </label>
            <input
              id="grant-remote-users"
              value={isTunnel(accessType) ? "" : remoteUsers}
              onChange={(e) => setRemoteUsers(e.target.value)}
              disabled={isTunnel(accessType)}
              placeholder={targetField(accessType, true).placeholder}
            />
          </div>
          <div>
            <label className="field">TTL hours (0 = no expiry)</label>
            <input type="number" min={0} value={durationHours} onChange={(e) => setDurationHours(Number(e.target.value) || 0)} />
          </div>
        </div>
        <button className="btn sm" type="submit" style={{ marginTop: "0.75rem" }} disabled={grant.isPending}>
          Grant
        </button>
      </form>
      <div className="card">
        <div className="row" style={{ marginBottom: "0.75rem", alignItems: "flex-end" }}>
          <div>
            <label className="field">View grants by</label>
            <select
              value={viewMode}
              onChange={(e) => {
                setViewMode(e.target.value as ViewMode);
                table.setPage(0);
              }}
            >
              <option value="all">All grants</option>
              <option value="user">User</option>
              <option value="machine">Machine</option>
            </select>
          </div>
          {viewMode === "user" ? (
            <div>
              <label className="field">User</label>
              <select value={viewUserId} onChange={(e) => setViewUserId(e.target.value)}>
                <option value="">Select…</option>
                {(usersQ.data || []).map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.username}
                  </option>
                ))}
              </select>
            </div>
          ) : null}
          {viewMode === "machine" ? (
            <div>
              <label className="field">Machine</label>
              <select value={viewMachineId} onChange={(e) => setViewMachineId(e.target.value)}>
                <option value="">Select…</option>
                {(machinesQ.data || []).map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </select>
            </div>
          ) : null}
        </div>
        <h3>
          {viewMode === "all"
            ? "All active grants"
            : `Grants for ${viewId ? (viewMode === "user" ? userName(viewId) : machineName(viewId)) : "…"}`}
        </h3>
        {viewMode !== "all" && !viewUserId && !viewMachineId ? (
          <div className="empty">Select a {viewMode} to list permissions.</div>
        ) : permsQ.isLoading ? (
          <p className="muted">Loading…</p>
        ) : perms.length === 0 ? (
          <div className="empty">No permissions.</div>
        ) : (
          <>
            <TableToolbar query={table.query} onQuery={table.setQuery} placeholder="Filter grants…" />
            <table>
              <thead>
                <tr>
                  {viewMode === "all" ? <th>User</th> : null}
                  <SortTh
                    label={viewMode === "machine" ? "User" : "Machine"}
                    col="peer"
                    sortKey={table.sortKey}
                    sortDir={table.sortDir}
                    onSort={table.toggleSort}
                  />
                  <SortTh label="Access" col="access" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                  <th>Remote users</th>
                  <SortTh label="Granted" col="granted" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                  <SortTh label="Expires" col="expires" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                  <th />
                </tr>
              </thead>
              <tbody>
                {processed.rows.map((p) => {
                  const peerLabel = viewMode === "machine" ? userName(p.user_id) : machineName(p.machine_id);
                  return (
                    <tr key={p.id}>
                      {viewMode === "all" ? <td>{userName(p.user_id)}</td> : null}
                      <td>{peerLabel}</td>
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
                          <button type="button" className="btn danger sm" onClick={() => void revoke(p.id, peerLabel)}>
                            Revoke
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            <Pagination
              page={processed.page}
              pageCount={processed.pageCount}
              total={processed.total}
              pageSize={table.pageSize}
              onPage={table.setPage}
            />
          </>
        )}
      </div>
    </>
  );
}
