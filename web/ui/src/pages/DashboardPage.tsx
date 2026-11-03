import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth, useRole } from "../auth/AuthContext";
import { api } from "../lib/api";
import type { AccessRequest, Machine, Session, UsageDashboard } from "../lib/types";
import { Badge } from "../components/Badge";
import { fmtTime, shortId } from "../lib/format";

type SetupStatus = {
  complete?: boolean;
  next?: string;
  counts?: { connected_agents?: number; machines?: number };
  steps?: {
    admin_exists?: boolean;
    has_machines?: boolean;
    has_connected_agents?: boolean;
    has_users?: boolean;
    has_permissions?: boolean;
  };
};

function fmtSeconds(seconds: number | undefined) {
  if (!seconds || seconds <= 0) return "—";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  return `${(seconds / 3600).toFixed(1)}h`;
}

export function DashboardPage() {
  const { user } = useAuth();
  const role = useRole();
  const isOps = role === "admin" || role === "operator";
  const [windowHours, setWindowHours] = useState(24);

  const machines = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });
  const sessions = useQuery({
    queryKey: ["sessions", "active"],
    queryFn: () => api<Session[]>("/sessions/active"),
  });
  const pending = useQuery({
    queryKey: ["requests", "pending"],
    queryFn: () => api<AccessRequest[]>("/access-requests?status=pending"),
    enabled: isOps,
  });
  const setup = useQuery({
    queryKey: ["setup"],
    queryFn: () => api<SetupStatus>("/setup/status"),
    enabled: isOps,
  });
  const usage = useQuery({
    queryKey: ["dashboard", "usage", windowHours],
    queryFn: () => api<UsageDashboard>(`/dashboard/usage?window_hours=${windowHours}&top=5`),
    enabled: isOps,
    refetchInterval: 30_000,
  });
  const agents = useQuery({
    queryKey: ["agents", "connected"],
    queryFn: async () => {
      const data = await api<{ agents?: string[] } | string[]>("/admin/agents/connected");
      if (Array.isArray(data)) return data;
      return data.agents || [];
    },
    enabled: isOps,
    refetchInterval: 15_000,
  });

  const machineList = machines.data || [];
  const connected = new Set((agents.data || []).map(String));
  const onlineCount = isOps
    ? machineList.filter((m) => connected.has(m.id) || connected.has(m.name)).length
    : null;
  const activeSessions = sessions.data || [];
  const setupIncomplete = isOps && setup.data && setup.data.complete === false;
  const usageVolume = usage.data?.access_volume;
  const usageLatency = usage.data?.approval_latency;
  const usageTopTargets = usage.data?.top_targets || [];
  const selectedWindowLabel = useMemo(() => {
    if (windowHours < 24) return `${windowHours}h`;
    if (windowHours % 24 === 0) return `${windowHours / 24}d`;
    return `${windowHours}h`;
  }, [windowHours]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Dashboard</h1>
          <p>Hi {user?.username} — here’s what’s running.</p>
        </div>
        <div className="row">
          <Link className="btn secondary sm" to="/sessions">
            Sessions
          </Link>
          {isOps ? (
            <Link className="btn secondary sm" to="/agents">
              Agents
            </Link>
          ) : null}
        </div>
      </div>
      {setupIncomplete ? (
        <div className="card" style={{ marginBottom: "1rem" }}>
          <h3>Setup incomplete</h3>
          <p className="muted">{setup.data?.next || "Finish the remaining setup steps."}</p>
          <Link className="btn sm" to="/setup">
            Setup checklist
          </Link>
        </div>
      ) : null}
      <div className="grid">
        <div className="card">
          <div className="stat-label">Machines</div>
          <div className="stat">{machines.data?.length ?? "—"}</div>
        </div>
        <div className="card">
          <div className="stat-label">Active sessions</div>
          <div className="stat">{sessions.data?.length ?? "—"}</div>
        </div>
        {isOps ? (
          <>
            <div className="card">
              <div className="stat-label">Agents online</div>
              <div className="stat">
                {agents.isLoading ? "…" : `${onlineCount ?? 0}/${machineList.length}`}
              </div>
            </div>
            <div className="card">
              <div className="stat-label">Pending requests</div>
              <div className="stat">{pending.data?.length ?? "—"}</div>
            </div>
          </>
        ) : (
          <div className="card">
            <div className="stat-label">Role</div>
            <div className="stat" style={{ fontSize: "1.25rem", textTransform: "capitalize" }}>
              {role}
            </div>
          </div>
        )}
      </div>

      {isOps ? (
        <div className="card" style={{ marginBottom: "1rem" }}>
          <div className="page-head" style={{ marginBottom: "0.75rem" }}>
            <div>
              <h3 style={{ marginBottom: "0.3rem" }}>Access analytics</h3>
              <p className="muted" style={{ margin: 0 }}>
                Rolling {selectedWindowLabel} operational view, auto-refreshed every 30 seconds.
              </p>
            </div>
            <div className="row" style={{ minWidth: 200 }}>
              <select
                value={windowHours}
                onChange={(e) => setWindowHours(Number(e.target.value) || 24)}
                aria-label="Analytics window"
              >
                <option value={6}>Last 6h</option>
                <option value={24}>Last 24h</option>
                <option value={72}>Last 3d</option>
                <option value={168}>Last 7d</option>
                <option value={336}>Last 14d</option>
              </select>
            </div>
          </div>
          {usage.isError ? <div className="err">Failed to load usage analytics.</div> : null}
          <div className="grid" style={{ marginBottom: "0.8rem" }}>
            <div>
              <div className="stat-label">Session starts</div>
              <div className="stat">{usageVolume?.sessions_total ?? "—"}</div>
            </div>
            <div>
              <div className="stat-label">Access requests</div>
              <div className="stat">{usageVolume?.requests_total ?? "—"}</div>
            </div>
            <div>
              <div className="stat-label">Approval latency avg</div>
              <div className="stat">{fmtSeconds(usageLatency?.average_seconds)}</div>
            </div>
            <div>
              <div className="stat-label">Latency p95</div>
              <div className="stat">{fmtSeconds(usageLatency?.p95_seconds)}</div>
            </div>
          </div>
          <div className="grid" style={{ gridTemplateColumns: "1fr 1fr", marginBottom: "0.75rem" }}>
            <div>
              <div className="muted" style={{ fontSize: "0.82rem", marginBottom: "0.35rem" }}>
                Request status mix
              </div>
              <div className="row" style={{ gap: "0.35rem" }}>
                <Badge status="ok">approved {usageVolume?.requests_approved ?? 0}</Badge>
                <Badge status="warn">pending {usageVolume?.requests_pending ?? 0}</Badge>
                <Badge status="danger">rejected {usageVolume?.requests_rejected ?? 0}</Badge>
              </div>
            </div>
            <div>
              <div className="muted" style={{ fontSize: "0.82rem", marginBottom: "0.35rem" }}>
                Active sessions right now
              </div>
              <div className="row" style={{ gap: "0.35rem" }}>
                <Badge status="ok">{usageVolume?.sessions_active ?? 0} active</Badge>
                <span className="muted" style={{ fontSize: "0.84rem" }}>
                  based on sessions started in this window
                </span>
              </div>
            </div>
          </div>
          <h3 style={{ marginTop: "0.2rem" }}>Most-accessed targets</h3>
          {usageTopTargets.length === 0 ? (
            <div className="empty">No target access activity in this window.</div>
          ) : (
            <section className="table-scroll" tabIndex={0} aria-label="Most-accessed targets">
              <table>
                <thead>
                  <tr>
                    <th>Target</th>
                    <th>Session starts</th>
                  </tr>
                </thead>
                <tbody>
                  {usageTopTargets.map((target) => (
                    <tr key={target.machine_id}>
                      <td>{target.machine_name}</td>
                      <td className="mono">{target.session_count}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          )}
          <p className="muted" style={{ marginTop: "0.65rem", marginBottom: 0 }}>
            Updated {fmtTime(usage.data?.generated_at)}
          </p>
        </div>
      ) : null}

      {isOps ? (
        <div className="grid" style={{ marginTop: "1rem", gridTemplateColumns: "1fr 1fr" }}>
          <div className="card">
            <h3>Agent health</h3>
            {machineList.length === 0 ? (
              <div className="empty">No machines yet.</div>
            ) : (
              <section className="table-scroll" tabIndex={0} aria-label="Agent health">
                <table>
                  <thead>
                    <tr>
                      <th>Machine</th>
                      <th>Tunnel</th>
                    </tr>
                  </thead>
                  <tbody>
                    {machineList.slice(0, 8).map((m) => {
                      const up = connected.has(m.id) || connected.has(m.name);
                      return (
                        <tr key={m.id}>
                          <td>{m.name}</td>
                          <td>
                            <Badge status={up ? "online" : "offline"}>{up ? "online" : "offline"}</Badge>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </section>
            )}
            {machineList.length > 8 ? (
              <p className="muted" style={{ marginTop: "0.65rem" }}>
                Showing 8 of {machineList.length} — see Agents for the full list.
              </p>
            ) : null}
            <Link className="btn secondary sm" to="/agents" style={{ marginTop: "0.75rem" }}>
              Manage agents
            </Link>
          </div>
          <div className="card">
            <h3>Active sessions</h3>
            {activeSessions.length === 0 ? (
              <div className="empty">Nothing active right now.</div>
            ) : (
              <section className="table-scroll" tabIndex={0} aria-label="Active sessions">
                <table>
                  <thead>
                    <tr>
                      <th>ID</th>
                      <th>Started</th>
                      <th>Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {activeSessions.slice(0, 8).map((s) => (
                      <tr key={s.id}>
                        <td className="mono">{shortId(s.id)}</td>
                        <td className="mono">{fmtTime(s.start_time)}</td>
                        <td>
                          <Badge status={s.status}>{s.status || "active"}</Badge>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </section>
            )}
            <Link className="btn secondary sm" to="/sessions" style={{ marginTop: "0.75rem" }}>
              View all sessions
            </Link>
          </div>
        </div>
      ) : null}
    </>
  );
}
