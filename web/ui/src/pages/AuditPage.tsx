import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, apiDownload } from "../lib/api";
import type { AuditLog } from "../lib/types";
import { fmtTime, shortId } from "../lib/format";
import { Pagination, SortTh, useTableState } from "../components/DataTable";
import { useUserDirectory } from "../lib/useUserDirectory";

export function AuditPage() {
  const [actionFilter, setActionFilter] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  const [limit, setLimit] = useState(200);
  const [exportErr, setExportErr] = useState("");
  const logs = useQuery({
    queryKey: ["audit", limit],
    queryFn: () => api<AuditLog[]>(`/audit-logs?limit=${limit}`),
  });
  const users = useUserDirectory();
  const table = useTableState<AuditLog>({ pageSize: 25 });

  const userName = (id?: string) => {
    if (!id) return "—";
    return users.data?.find((u) => u.id === id)?.username || shortId(id);
  };

  const actions = useMemo(() => {
    const set = new Set<string>();
    for (const l of logs.data || []) if (l.action) set.add(l.action);
    return [...set].sort();
  }, [logs.data]);

  const filtered = useMemo(() => {
    let rows = logs.data || [];
    if (actionFilter) rows = rows.filter((l) => l.action === actionFilter);
    if (actorFilter) {
      rows = rows.filter((l) => {
        const name = userName(l.user_id).toLowerCase();
        return name.includes(actorFilter.toLowerCase()) || (l.user_id || "").includes(actorFilter);
      });
    }
    return rows;
  }, [logs.data, actionFilter, actorFilter, users.data]);

  const processed = useMemo(() => {
    return table.process(
      filtered,
      (l, key) => {
        if (key === "time") return l.timestamp ? new Date(l.timestamp).getTime() : 0;
        if (key === "actor") return userName(l.user_id);
        if (key === "action") return l.action;
        if (key === "resource") return l.resource || "";
        if (key === "ip") return l.ip_address || "";
        return "";
      },
      (l) => `${userName(l.user_id)} ${l.action} ${l.resource || ""} ${l.ip_address || ""} ${l.id}`,
    );
  }, [filtered, table.query, table.sortKey, table.sortDir, table.page, table.pageSize, users.data]);

  async function exportAudit(format: "pdf" | "csv" | "json") {
    setExportErr("");
    const params = new URLSearchParams({ format, limit: String(limit) });
    if (actionFilter) params.set("action", actionFilter);
    if (actorFilter.trim()) params.set("actor", actorFilter.trim());
    if (table.query.trim()) params.set("q", table.query.trim());

    try {
      await apiDownload(`/reports/audit/export?${params.toString()}`, `audit.${format}`);
    } catch (e) {
      setExportErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Audit</h1>
          <p>What happened, when, and who did it.</p>
        </div>
        <div className="row">
          <button type="button" className="btn secondary sm" onClick={() => void exportAudit("csv")}>
            Export CSV
          </button>
          <button type="button" className="btn secondary sm" onClick={() => void exportAudit("pdf")}>
            Export PDF
          </button>
          <button type="button" className="btn secondary sm" onClick={() => void exportAudit("json")}>
            Export JSON
          </button>
          <button type="button" className="btn secondary sm" onClick={() => void logs.refetch()}>
            Refresh
          </button>
        </div>
      </div>
      <div className="card">
        {exportErr ? <div className="err">{exportErr}</div> : null}
        <div className="table-toolbar">
          <input
            className="table-search"
            value={table.query}
            onChange={(e) => table.setQuery(e.target.value)}
            placeholder="Search logs…"
          />
          <select value={actionFilter} onChange={(e) => { setActionFilter(e.target.value); table.setPage(0); }}>
            <option value="">All actions</option>
            {actions.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
          </select>
          <input
            value={actorFilter}
            onChange={(e) => { setActorFilter(e.target.value); table.setPage(0); }}
            placeholder="Actor…"
            style={{ maxWidth: 160 }}
          />
          <select value={limit} onChange={(e) => setLimit(Number(e.target.value) || 200)}>
            <option value={50}>50</option>
            <option value={100}>100</option>
            <option value={200}>200</option>
            <option value={500}>500</option>
          </select>
        </div>
        <table>
          <thead>
            <tr>
              <SortTh label="Time" col="time" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="Actor" col="actor" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="Action" col="action" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="Resource" col="resource" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="IP" col="ip" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
            </tr>
          </thead>
          <tbody>
            {processed.rows.length === 0 ? (
              <tr>
                <td colSpan={5} className="empty">
                  No audit events.
                </td>
              </tr>
            ) : (
              processed.rows.map((l) => (
                <tr key={l.id}>
                  <td className="mono">{fmtTime(l.timestamp)}</td>
                  <td>{userName(l.user_id)}</td>
                  <td className="mono">{l.action}</td>
                  <td className="mono">{l.resource || "—"}</td>
                  <td className="mono">{l.ip_address || "—"}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
        <Pagination
          page={processed.page}
          pageCount={processed.pageCount}
          total={processed.total}
          pageSize={table.pageSize}
          onPage={table.setPage}
        />
      </div>
    </>
  );
}
