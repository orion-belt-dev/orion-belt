import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { Machine } from "../lib/types";
import { Badge } from "../components/Badge";
import { useToast } from "../components/Toast";
import { fmtTime, shortId } from "../lib/format";
import { Pagination, SortTh, TableToolbar, useTableState } from "../components/DataTable";

function tagStatus(m: Machine) {
  return (m.tags && m.tags.status) || "";
}

function isRevoked(m: Machine) {
  return tagStatus(m) === "revoked";
}

function isPaused(m: Machine) {
  return tagStatus(m) === "paused" || (!m.is_active && !isRevoked(m) && tagStatus(m) !== "archived");
}

export function AgentsPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const table = useTableState<Machine>({ pageSize: 25 });
  const [editing, setEditing] = useState<Machine | null>(null);
  const [editName, setEditName] = useState("");
  const [editHost, setEditHost] = useState("");
  const [editPort, setEditPort] = useState(22);
  const [cmdOut, setCmdOut] = useState("");

  const connectedQ = useQuery({
    queryKey: ["agents", "connected"],
    queryFn: async () => {
      const data = await api<{ agents?: string[] } | string[]>("/admin/agents/connected");
      if (Array.isArray(data)) return data;
      return data.agents || [];
    },
    refetchInterval: 10_000,
  });
  const machinesQ = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });

  const connected = new Set((connectedQ.data || []).map(String));
  const machines = machinesQ.data || [];

  const processed = useMemo(() => {
    return table.process(
      machines,
      (m, key) => {
        if (key === "name") return m.name;
        if (key === "host") return `${m.hostname}:${m.port}`;
        if (key === "tunnel") return connected.has(m.id) || connected.has(m.name) ? 1 : 0;
        if (key === "status") return tagStatus(m) || (m.is_active ? "active" : "inactive");
        return "";
      },
      (m) => `${m.name} ${m.hostname} ${m.id} ${tagStatus(m)}`,
    );
  }, [machines, connected, table.query, table.sortKey, table.sortDir, table.page, table.pageSize]);

  function refresh() {
    void connectedQ.refetch();
    void machinesQ.refetch();
    void qc.invalidateQueries({ queryKey: ["machines"] });
  }

  async function patchMachine(id: string, body: Record<string, unknown>, okMsg: string) {
    try {
      await api(`/admin/machines/${encodeURIComponent(id)}`, {
        method: "PUT",
        body: JSON.stringify(body),
      });
      toast(okMsg);
      refresh();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function sendCommand(id: string, command: string) {
    try {
      const res = await api<{ output?: string }>(`/admin/agents/${encodeURIComponent(id)}/command`, {
        method: "POST",
        body: JSON.stringify({ command }),
      });
      setCmdOut(res.output || "(no output)");
      toast(`Sent ${command}`);
      refresh();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function disconnect(id: string) {
    try {
      await api(`/admin/agents/${encodeURIComponent(id)}/disconnect`, { method: "POST", body: "{}" });
      toast("Agent disconnected");
      refresh();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  function startEdit(m: Machine) {
    setEditing(m);
    setEditName(m.name);
    setEditHost(m.hostname);
    setEditPort(m.port);
  }

  async function saveEdit() {
    if (!editing) return;
    await patchMachine(
      editing.id,
      { name: editName, hostname: editHost, port: editPort },
      "Agent updated",
    );
    setEditing(null);
  }

  async function pause(m: Machine) {
    const tags = { ...(m.tags || {}), status: "paused" };
    await patchMachine(m.id, { is_active: false, tags }, "Agent paused (sessions blocked)");
  }

  async function resume(m: Machine) {
    const tags = { ...(m.tags || {}) };
    delete tags.status;
    await patchMachine(m.id, { is_active: true, tags }, "Agent resumed");
  }

  async function revoke(m: Machine) {
    if (!confirm(`Revoke agent ${m.name}? It cannot connect until re-enrolled.`)) return;
    const tags = { ...(m.tags || {}), status: "revoked" };
    await patchMachine(m.id, { is_active: false, tags }, "Agent revoked");
    if (connected.has(m.id) || connected.has(m.name)) {
      try {
        await api(`/admin/agents/${encodeURIComponent(m.id)}/disconnect`, { method: "POST", body: "{}" });
      } catch {
        /* offline is fine */
      }
    }
    refresh();
  }

  async function archive(m: Machine) {
    if (!confirm(`Archive ${m.name}?`)) return;
    const tags = { ...(m.tags || {}), status: "archived" };
    await patchMachine(m.id, { is_active: false, tags }, "Agent archived");
  }

  async function remove(m: Machine) {
    if (!isRevoked(m) && tagStatus(m) !== "archived") {
      toast("Delete is only allowed after revoke (or archive)", "err");
      return;
    }
    if (
      !confirm(
        `Permanently delete ${m.name}?\n\nThis removes related permissions, access requests, session history, and recordings for this agent.\n\nIf you need to keep those records, cancel and Archive the agent instead.`,
      )
    ) {
      return;
    }
    try {
      await api(`/admin/machines/${encodeURIComponent(m.id)}`, { method: "DELETE" });
      toast("Agent deleted");
      refresh();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  const onlineCount = machines.filter((m) => connected.has(m.id) || connected.has(m.name)).length;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Agents</h1>
          <p>Connected tunnels back to this gateway.</p>
        </div>
        <div className="row">
          <Link className="btn sm" to="/add-agent">
            Add agent
          </Link>
          <button type="button" className="btn secondary sm" onClick={refresh}>
            Refresh
          </button>
        </div>
      </div>
      <div className="grid">
        <div className="card">
          <div className="stat-label">Connected now</div>
          <div className="stat">{onlineCount}</div>
        </div>
        <div className="card">
          <div className="stat-label">Registered</div>
          <div className="stat">{machines.length}</div>
        </div>
      </div>

      {editing ? (
        <div className="card" style={{ marginBottom: "1rem" }}>
          <h3>Edit {editing.name}</h3>
          <div className="form-grid">
            <div>
              <label className="field">Name</label>
              <input value={editName} onChange={(e) => setEditName(e.target.value)} />
            </div>
            <div>
              <label className="field">Hostname</label>
              <input value={editHost} onChange={(e) => setEditHost(e.target.value)} />
            </div>
            <div>
              <label className="field">Port</label>
              <input type="number" value={editPort} onChange={(e) => setEditPort(Number(e.target.value) || 22)} />
            </div>
          </div>
          <div className="row" style={{ marginTop: "0.75rem" }}>
            <button type="button" className="btn sm" onClick={() => void saveEdit()}>
              Save
            </button>
            <button type="button" className="btn secondary sm" onClick={() => setEditing(null)}>
              Cancel
            </button>
          </div>
        </div>
      ) : null}

      <div className="card">
        <TableToolbar query={table.query} onQuery={table.setQuery} placeholder="Filter agents…" />
        <table>
          <thead>
            <tr>
              <SortTh label="Name" col="name" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="Host" col="host" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="Tunnel" col="tunnel" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="State" col="status" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <th>Last seen</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {processed.rows.map((m) => {
              const up = connected.has(m.id) || connected.has(m.name);
              const revoked = isRevoked(m);
              const paused = isPaused(m);
              const archived = tagStatus(m) === "archived";
              let state = "registered";
              if (revoked) state = "revoked";
              else if (archived) state = "archived";
              else if (paused) state = "paused";
              else if (up) state = "online";
              else state = "offline";
              return (
                <tr key={m.id}>
                  <td>
                    <strong>{m.name}</strong>
                    <div className="mono muted" style={{ fontSize: ".72rem" }}>
                      {shortId(m.id)}
                    </div>
                  </td>
                  <td className="mono">
                    {m.hostname}:{m.port}
                  </td>
                  <td>
                    <Badge status={up ? "online" : "offline"}>{up ? "online" : "offline"}</Badge>
                  </td>
                  <td>
                    <Badge status={state}>{state}</Badge>
                  </td>
                  <td className="mono">{fmtTime(m.last_seen_at)}</td>
                  <td>
                    <div className="row agent-actions">
                      <button type="button" className="btn secondary sm" onClick={() => startEdit(m)}>
                        Edit
                      </button>
                      {paused || (!m.is_active && !revoked && !archived) ? (
                        <button type="button" className="btn secondary sm" onClick={() => void resume(m)}>
                          Resume
                        </button>
                      ) : (
                        <button type="button" className="btn secondary sm" disabled={revoked || archived} onClick={() => void pause(m)}>
                          Pause
                        </button>
                      )}
                      <button type="button" className="btn secondary sm" disabled={!up} onClick={() => void disconnect(m.id)}>
                        Disconnect
                      </button>
                      <button type="button" className="btn secondary sm" disabled={!up} onClick={() => void sendCommand(m.id, "orion:restart")}>
                        Restart
                      </button>
                      <button type="button" className="btn secondary sm" disabled={revoked} onClick={() => void revoke(m)}>
                        Revoke
                      </button>
                      <button type="button" className="btn secondary sm" disabled={archived} onClick={() => void archive(m)}>
                        Archive
                      </button>
                      <button
                        type="button"
                        className="btn danger sm"
                        disabled={!revoked && !archived}
                        title={revoked || archived ? "Delete permanently" : "Revoke first"}
                        onClick={() => void remove(m)}
                      >
                        Delete
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
        {cmdOut ? (
          <div style={{ marginTop: "0.85rem" }}>
            <label className="field">Last command output</label>
            <pre className="code">{cmdOut}</pre>
          </div>
        ) : null}
      </div>
    </>
  );
}
