import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "../components/Badge";
import { api, apiDownload, apiRaw } from "../lib/api";
import { fmtTime, shortId } from "../lib/format";
import type { Machine, Session } from "../lib/types";
import { CastPlayer } from "../components/CastPlayer";
import { LiveSessionWatch } from "../components/LiveSessionWatch";
import { Pagination, SortTh, TableToolbar, useTableState } from "../components/DataTable";
import { useUserDirectory } from "../lib/useUserDirectory";

export function SessionsPage() {
  const [filter, setFilter] = useState<"all" | "active" | "completed">("all");
  const [castText, setCastText] = useState<string | null>(null);
  const [playId, setPlayId] = useState<string | null>(null);
  const [watchId, setWatchId] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const [exportErr, setExportErr] = useState("");
  const table = useTableState<Session>({ pageSize: 25 });

  const sessionsQ = useQuery({
    queryKey: ["sessions", filter],
    queryFn: async () => {
      if (filter === "active") return api<Session[]>("/sessions/active");
      const all = await api<Session[]>("/sessions");
      if (filter === "completed") return (all || []).filter((s) => s.status === "completed");
      return all || [];
    },
  });
  const usersQ = useUserDirectory();
  const machinesQ = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });

  const userName = useMemo(() => {
    const map = new Map((usersQ.data || []).map((u) => [u.id, u.username]));
    return (id: string) => map.get(id) || shortId(id);
  }, [usersQ.data]);
  const machineName = useMemo(() => {
    const map = new Map((machinesQ.data || []).map((m) => [m.id, m.name]));
    return (id: string) => map.get(id) || shortId(id);
  }, [machinesQ.data]);

  const processed = useMemo(() => {
    return table.process(
      sessionsQ.data || [],
      (s, key) => {
        if (key === "id") return s.id;
        if (key === "user") return userName(s.user_id);
        if (key === "machine") return machineName(s.machine_id);
        if (key === "remote") return s.remote_user || "";
        if (key === "source") return s.source || "";
        if (key === "started") return s.start_time ? new Date(s.start_time).getTime() : 0;
        if (key === "ended") return s.end_time ? new Date(s.end_time).getTime() : 0;
        if (key === "status") return s.status || "";
        return "";
      },
      (s) =>
        `${s.id} ${userName(s.user_id)} ${machineName(s.machine_id)} ${s.remote_user || ""} ${s.source || ""} ${s.status || ""}`,
    );
  }, [sessionsQ.data, table.query, table.sortKey, table.sortDir, table.page, table.pageSize, userName, machineName]);

  async function play(id: string) {
    setErr("");
    setWatchId(null);
    setPlayId(id);
    setCastText(null);
    try {
      const res = await apiRaw(`/sessions/${encodeURIComponent(id)}/content`);
      const text = await res.text();
      if (!res.ok) {
        let msg = text;
        try {
          msg = JSON.parse(text).error || text;
        } catch {
          /* ignore */
        }
        throw new Error(msg || "failed to load recording");
      }
      setCastText(text);
      window.setTimeout(() => {
        document.getElementById("playback-panel")?.scrollIntoView({ behavior: "smooth", block: "nearest" });
      }, 50);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function exportSessions(format: "pdf" | "csv" | "json") {
    setExportErr("");
    const params = new URLSearchParams({ format });
    if (filter !== "all") params.set("status", filter);
    if (table.query.trim()) params.set("q", table.query.trim());

    try {
      await apiDownload(`/reports/sessions/export?${params.toString()}`, `sessions.${format}`);
    } catch (e) {
      setExportErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Sessions</h1>
          <p>Live sessions and recordings you can replay.</p>
        </div>
        <div className="row">
          {(["all", "active", "completed"] as const).map((f) => (
            <button key={f} type="button" className={`btn sm${filter === f ? "" : " secondary"}`} onClick={() => setFilter(f)}>
              {f[0].toUpperCase() + f.slice(1)}
            </button>
          ))}
          <button type="button" className="btn secondary sm" onClick={() => void sessionsQ.refetch()}>
            Refresh
          </button>
          <button type="button" className="btn secondary sm" onClick={() => void exportSessions("csv")}>
            Export CSV
          </button>
          <button type="button" className="btn secondary sm" onClick={() => void exportSessions("pdf")}>
            Export PDF
          </button>
          <button type="button" className="btn secondary sm" onClick={() => void exportSessions("json")}>
            Export JSON
          </button>
        </div>
      </div>
      <div className="card">
        {exportErr ? <div className="err">{exportErr}</div> : null}
        <TableToolbar query={table.query} onQuery={table.setQuery} placeholder="Filter by user, machine, status, ID…" />
        {processed.total ? (
          <table>
            <thead>
              <tr>
                <SortTh label="ID" col="id" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="User" col="user" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Machine" col="machine" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Remote" col="remote" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Source" col="source" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Started" col="started" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Ended" col="ended" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Status" col="status" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <th />
              </tr>
            </thead>
            <tbody>
              {processed.rows.map((s) => (
                <tr key={s.id}>
                  <td className="mono">{shortId(s.id)}</td>
                  <td>{userName(s.user_id)}</td>
                  <td>{machineName(s.machine_id)}</td>
                  <td className="mono">{s.remote_user || "—"}</td>
                  <td>
                    <Badge status={s.source || "ssh"}>{s.source || "ssh"}</Badge>
                  </td>
                  <td className="mono">{fmtTime(s.start_time)}</td>
                  <td className="mono">{fmtTime(s.end_time)}</td>
                  <td>
                    <Badge status={s.status}>{s.status}</Badge>
                  </td>
                  <td>
                    <div className="row">
                      {s.status === "active" ? (
                        <button
                          type="button"
                          className="btn sm"
                          onClick={() => {
                            setPlayId(null);
                            setCastText(null);
                            setWatchId(s.id);
                            window.setTimeout(() => {
                              document.getElementById("watch-panel")?.scrollIntoView({ behavior: "smooth", block: "nearest" });
                            }, 50);
                          }}
                        >
                          Watch
                        </button>
                      ) : null}
                      <button type="button" className="btn secondary sm" disabled={!s.recording_path} onClick={() => void play(s.id)}>
                        Playback
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty">{table.query.trim() ? "No sessions match your search." : "No sessions."}</div>
        )}
        <Pagination
          page={processed.page}
          pageCount={processed.pageCount}
          total={processed.total}
          pageSize={table.pageSize}
          onPage={table.setPage}
        />
        {watchId ? <LiveSessionWatch sessionId={watchId} onClose={() => setWatchId(null)} /> : null}
        <div className="playback-panel" id="playback-panel">
          {err ? <div className="err">{err}</div> : null}
          {playId && castText !== null ? (
            <CastPlayer sessionId={playId} text={castText} />
          ) : playId ? (
            <p className="muted">Loading recording…</p>
          ) : null}
        </div>
      </div>
    </>
  );
}
