import { useMemo, useState } from "react";
import type { FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { Machine } from "../lib/types";
import { Badge } from "../components/Badge";
import { useToast } from "../components/Toast";
import { canApprove } from "../lib/nav";
import { useAuth } from "../auth/AuthContext";
import { Pagination, SortTh, TableToolbar, useTableState } from "../components/DataTable";

export function MachinesPage() {
  const { user } = useAuth();
  const manage = canApprove(user);
  const qc = useQueryClient();
  const { toast } = useToast();
  const q = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });
  const table = useTableState<Machine>({ pageSize: 25 });
  const [name, setName] = useState("");
  const [hostname, setHostname] = useState("");
  const [port, setPort] = useState(22);

  const create = useMutation({
    mutationFn: () =>
      api("/admin/machines", {
        method: "POST",
        body: JSON.stringify({ name, hostname: hostname || name, port }),
      }),
    onSuccess: () => {
      toast("Machine created");
      setName("");
      setHostname("");
      void qc.invalidateQueries({ queryKey: ["machines"] });
    },
    onError: (e: Error) => toast(e.message, "err"),
  });

  function onCreate(e: FormEvent) {
    e.preventDefault();
    create.mutate();
  }

  const processRows = table.process;
  const processed = useMemo(() => {
    return processRows(
      q.data || [],
      (m, key) => {
        if (key === "name") return m.name;
        if (key === "host") return `${m.hostname}:${m.port}`;
        if (key === "status") return m.is_active ? 1 : 0;
        return "";
      },
      (m) => `${m.name} ${m.hostname} ${m.port} ${m.id}`,
    );
  }, [q.data, processRows]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Machines</h1>
          <p>Hosts you can grant access to. Use Agents to connect or disconnect them.</p>
        </div>
      </div>
      {manage ? (
        <form className="card" onSubmit={onCreate} style={{ marginBottom: "1rem" }}>
          <h3>Create machine</h3>
          <div className="form-grid">
            <div>
              <label className="field" htmlFor="machines-name">Name</label>
              <input id="machines-name" value={name} onChange={(e) => setName(e.target.value)} required />
            </div>
            <div>
              <label className="field" htmlFor="machines-hostname">Hostname</label>
              <input id="machines-hostname" value={hostname} onChange={(e) => setHostname(e.target.value)} />
            </div>
            <div>
              <label className="field" htmlFor="machines-port">Port</label>
              <input id="machines-port" type="number" value={port} onChange={(e) => setPort(Number(e.target.value) || 22)} />
            </div>
          </div>
          <button className="btn sm" type="submit" style={{ marginTop: "0.75rem" }}>
            Create
          </button>
        </form>
      ) : null}
      <div className="card">
        <TableToolbar query={table.query} onQuery={table.setQuery} placeholder="Filter machines…" />
        <section className="table-scroll" tabIndex={0} aria-label="Machines">
          <table>
            <thead>
              <tr>
                <SortTh label="Name" col="name" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Host" col="host" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Status" col="status" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              </tr>
            </thead>
            <tbody>
              {processed.rows.map((m) => (
                <tr key={m.id}>
                  <td>{m.name}</td>
                  <td className="mono">
                    {m.hostname}:{m.port}
                  </td>
                  <td>
                    <Badge status={m.is_active ? "online" : "offline"}>{m.is_active ? "online" : "offline"}</Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
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
