import { Fragment, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { Capability, CapabilityCatalog, CapabilityStatus } from "../lib/types";
import { Badge } from "../components/Badge";
import { fmtTime } from "../lib/format";
import { useToast } from "../components/Toast";
import { Pagination, SortTh, TableToolbar, useTableState } from "../components/DataTable";

type Scope = "related" | "all";

const DEFAULT_TTL_SECONDS = 30 * 60;

const TTL_OPTIONS = [
  { label: "15 minutes", value: 15 * 60 },
  { label: "30 minutes (default)", value: 30 * 60 },
  { label: "1 hour", value: 60 * 60 },
  { label: "4 hours", value: 4 * 60 * 60 },
  { label: "8 hours", value: 8 * 60 * 60 },
  { label: "24 hours", value: 24 * 60 * 60 },
  { label: "Unlimited", value: 0 },
];

const STATUS_LABEL: Record<CapabilityStatus, string> = {
  granted: "granted",
  pending: "pending",
  requestable: "can request",
};

// Badge colours come from the shared status vocabulary in lib/format.
const STATUS_TONE: Record<CapabilityStatus, string> = {
  granted: "active",
  pending: "pending",
  requestable: "neutral",
};

const ANY_LOGIN = "any login";

type Draft = { remoteUser: string; accessType: string; reason: string; duration: number };

function draftFor(cap: Capability): Draft {
  return {
    remoteUser: cap.remote_user,
    accessType: cap.access_type || "both",
    reason: "",
    duration: DEFAULT_TTL_SECONDS,
  };
}

export function CatalogPage() {
  const { toast } = useToast();
  const qc = useQueryClient();
  const table = useTableState<Capability>({ pageSize: 25 });
  const [scope, setScope] = useState<Scope>("related");
  const [statusFilter, setStatusFilter] = useState("");
  const [openId, setOpenId] = useState("");
  const [draft, setDraft] = useState<Draft>({ remoteUser: "", accessType: "both", reason: "", duration: DEFAULT_TTL_SECONDS });

  const catalog = useQuery({
    queryKey: ["capabilities", scope],
    queryFn: () => api<CapabilityCatalog>(`/capabilities?scope=${scope}`),
  });

  const request = useMutation({
    mutationFn: (cap: Capability) =>
      api("/access-requests", {
        method: "POST",
        body: JSON.stringify({
          machine_id: cap.machine_id,
          remote_users: [draft.remoteUser.trim()].filter(Boolean),
          access_type: draft.accessType,
          reason: draft.reason,
          duration: draft.duration,
        }),
      }),
    onSuccess: () => {
      toast("Request submitted");
      setOpenId("");
      void qc.invalidateQueries({ queryKey: ["capabilities"] });
      void qc.invalidateQueries({ queryKey: ["requests"] });
    },
    onError: (e: Error) => toast(e.message, "err"),
  });

  function openRequest(cap: Capability) {
    setOpenId(cap.id);
    setDraft(draftFor(cap));
  }

  function submitRequest(e: FormEvent, cap: Capability) {
    e.preventDefault();
    if (!draft.remoteUser.trim()) {
      toast("Name the remote user to log in as", "err");
      return;
    }
    request.mutate(cap);
  }

  const rows = useMemo(() => {
    let list = catalog.data?.capabilities || [];
    if (statusFilter) list = list.filter((c) => c.status === statusFilter);
    return list;
  }, [catalog.data, statusFilter]);

  const processed = useMemo(() => {
    return table.process(
      rows,
      (c, key) => {
        if (key === "machine") return c.machine_name;
        if (key === "remote") return c.remote_user;
        if (key === "status") return c.status;
        return "";
      },
      (c) => `${c.machine_name} ${c.hostname || ""} ${c.remote_user} ${c.access_type} ${c.status} ${c.reason}`,
    );
  }, [rows, table.query, table.sortKey, table.sortDir, table.page, table.pageSize]);

  const counts = useMemo(() => {
    const all = catalog.data?.capabilities || [];
    return {
      granted: all.filter((c) => c.status === "granted").length,
      pending: all.filter((c) => c.status === "pending").length,
      requestable: all.filter((c) => c.status === "requestable").length,
    };
  }, [catalog.data]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Access catalog</h1>
          <p>What you can reach today, and what you can ask for — no machine names to guess.</p>
        </div>
      </div>

      <div className="grid">
        <div className="card">
          <div className="stat-label">Granted</div>
          <div className="stat">{counts.granted}</div>
        </div>
        <div className="card">
          <div className="stat-label">Awaiting approval</div>
          <div className="stat">{counts.pending}</div>
        </div>
        <div className="card">
          <div className="stat-label">Can request</div>
          <div className="stat">{counts.requestable}</div>
        </div>
      </div>

      <div className="card">
        <TableToolbar query={table.query} onQuery={table.setQuery} placeholder="Filter catalog…">
          <div className="cap-filters">
            <select
              value={statusFilter}
              onChange={(e) => {
                setStatusFilter(e.target.value);
                table.setPage(0);
              }}
              aria-label="Filter by status"
            >
              <option value="">All statuses</option>
              <option value="granted">granted</option>
              <option value="pending">pending</option>
              <option value="requestable">can request</option>
            </select>
            <select
              value={scope}
              onChange={(e) => {
                setScope(e.target.value as Scope);
                setOpenId("");
                table.setPage(0);
              }}
              aria-label="Catalog scope"
            >
              <option value="related">Linked to me</option>
              <option value="all">Every machine</option>
            </select>
          </div>
        </TableToolbar>

        {catalog.isLoading ? (
          <p className="muted">Loading…</p>
        ) : catalog.isError ? (
          <div className="empty">{(catalog.error as Error).message}</div>
        ) : rows.length === 0 ? (
          <div className="empty">
            {scope === "related" ? (
              <>
                <p>Nothing is linked to your account yet.</p>
                <button type="button" className="btn sm" onClick={() => setScope("all")}>
                  Browse every machine
                </button>
              </>
            ) : (
              <p>No machines match this filter.</p>
            )}
          </div>
        ) : (
          <>
            <table>
              <thead>
                <tr>
                  <SortTh label="Machine" col="machine" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                  <SortTh label="Log in as" col="remote" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                  <th>Access</th>
                  <th>Why you see this</th>
                  <SortTh label="Status" col="status" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                  <th />
                </tr>
              </thead>
              <tbody>
                {processed.rows.map((cap) => (
                  <Fragment key={cap.id}>
                    <tr>
                      <td>
                        {cap.machine_name}
                        <div className="muted mono cap-host">
                          {cap.hostname || "—"}
                          {cap.machine_active ? "" : " · offline"}
                        </div>
                      </td>
                      <td className="mono">{cap.remote_user || ANY_LOGIN}</td>
                      <td>
                        <Badge status={cap.access_type}>{cap.access_type}</Badge>
                      </td>
                      <td>
                        <div>{cap.reason}</div>
                        <div className="muted cap-host">
                          {cap.status === "granted" && cap.expires_at ? `Until ${fmtTime(cap.expires_at)}` : null}
                          {cap.status === "granted" && !cap.expires_at ? "No expiry" : null}
                          {cap.last_used_at ? `Last used ${fmtTime(cap.last_used_at)}` : null}
                        </div>
                      </td>
                      <td>
                        <Badge status={STATUS_TONE[cap.status]}>{STATUS_LABEL[cap.status]}</Badge>
                      </td>
                      <td>
                        {cap.status === "granted" ? (
                          <Link
                            className="btn secondary sm"
                            to={`/terminal?machine=${encodeURIComponent(cap.machine_name)}&user=${encodeURIComponent(cap.remote_user)}`}
                          >
                            Open terminal
                          </Link>
                        ) : cap.status === "pending" ? (
                          <Link className="btn secondary sm" to="/requests">
                            View request
                          </Link>
                        ) : (
                          <button type="button" className="btn sm" onClick={() => openRequest(cap)}>
                            Request
                          </button>
                        )}
                      </td>
                    </tr>
                    {openId === cap.id ? (
                      <tr>
                        <td colSpan={6}>
                          <form className="cap-request" onSubmit={(e) => submitRequest(e, cap)}>
                            <h3>
                              Request {cap.access_type} on {cap.machine_name}
                            </h3>
                            <div className="form-grid">
                              <div>
                                <label className="field" htmlFor="cap-remote">
                                  Log in as
                                </label>
                                <input
                                  id="cap-remote"
                                  value={draft.remoteUser}
                                  onChange={(e) => setDraft((d) => ({ ...d, remoteUser: e.target.value }))}
                                  placeholder="root"
                                  required
                                />
                              </div>
                              <div>
                                <label className="field" htmlFor="cap-access">
                                  Access type
                                </label>
                                <select
                                  id="cap-access"
                                  value={draft.accessType}
                                  onChange={(e) => setDraft((d) => ({ ...d, accessType: e.target.value }))}
                                >
                                  <option value="ssh">ssh</option>
                                  <option value="scp">scp</option>
                                  <option value="both">both</option>
                                </select>
                              </div>
                              <div>
                                <label className="field" htmlFor="cap-reason">
                                  Reason
                                </label>
                                <input
                                  id="cap-reason"
                                  value={draft.reason}
                                  onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
                                  required
                                />
                              </div>
                              <div>
                                <label className="field" htmlFor="cap-duration">
                                  Access duration
                                </label>
                                <select
                                  id="cap-duration"
                                  value={draft.duration}
                                  onChange={(e) => setDraft((d) => ({ ...d, duration: Number(e.target.value) }))}
                                >
                                  {TTL_OPTIONS.map((o) => (
                                    <option key={o.value} value={o.value}>
                                      {o.label}
                                    </option>
                                  ))}
                                </select>
                              </div>
                            </div>
                            <div className="row" style={{ marginTop: "0.75rem" }}>
                              <button className="btn sm" type="submit" disabled={request.isPending}>
                                {request.isPending ? "Submitting…" : "Submit request"}
                              </button>
                              <button type="button" className="btn secondary sm" onClick={() => setOpenId("")}>
                                Cancel
                              </button>
                            </div>
                          </form>
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                ))}
              </tbody>
            </table>
            <Pagination
              page={processed.page}
              pageCount={processed.pageCount}
              total={processed.total}
              pageSize={table.pageSize}
              onPage={table.setPage}
            />
            {catalog.data?.truncated ? (
              <p className="muted">Showing the first {catalog.data.capabilities.length} entries — narrow the filter to see more.</p>
            ) : null}
          </>
        )}
      </div>
    </>
  );
}
