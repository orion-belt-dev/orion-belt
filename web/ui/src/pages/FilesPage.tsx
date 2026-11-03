import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, apiRaw } from "../lib/api";
import type { Machine } from "../lib/types";
import { useToast } from "../components/Toast";
import { fmtTime } from "../lib/format";
import { Pagination, SortTh, TableToolbar, useTableState } from "../components/DataTable";
import { useMemo } from "react";

const REMOTE_USERS = ["root", "ubuntu", "ec2-user", "admin", "deploy", "alpine", "opensuse", "debian"];

type Entry = { name: string; path: string; is_dir: boolean; size?: number; mtime?: number };

function joinPath(base: string, name: string) {
  if (!base || base === "/") return "/" + name.replace(/^\//, "");
  return base.replace(/\/$/, "") + "/" + name;
}

function parentPath(p: string) {
  if (!p || p === "/") return "/";
  const parts = p.replace(/\/$/, "").split("/");
  parts.pop();
  return parts.join("/") || "/";
}

function fmtSize(n?: number) {
  if (n == null || Number.isNaN(n)) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function FilesPage() {
  const { toast } = useToast();
  const machinesQ = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });
  const [machine, setMachine] = useState("");
  const [user, setUser] = useState("root");
  const [path, setPath] = useState("/");
  const [pathInput, setPathInput] = useState("/");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(false);
  const table = useTableState<Entry>({ pageSize: 50 });

  useEffect(() => {
    if (!machinesQ.data?.length || machine) return;
    setMachine(machinesQ.data[0].name);
  }, [machinesQ.data, machine]);

  async function load(p = pathInput) {
    if (!machine) return;
    setErr("");
    setLoading(true);
    try {
      const q = new URLSearchParams({ machine, user, path: p });
      const data = await api<{ entries?: Entry[]; error?: string; raw?: string } | Entry[]>(`/files/list?${q}`);
      if (data && !Array.isArray(data) && data.error) throw new Error(data.error);
      const list = Array.isArray(data) ? data : data.entries || [];
      setEntries(list);
      setPath(p);
      setPathInput(p);
      table.setQuery("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setEntries([]);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (machine) void load("/");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machine, user]);

  async function download(fp: string) {
    const q = new URLSearchParams({ machine, user, path: fp });
    const res = await apiRaw(`/files/download?${q}`);
    if (!res.ok) {
      toast("Download failed", "err");
      return;
    }
    const blob = await res.blob();
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = fp.split("/").pop() || "file";
    a.click();
    URL.revokeObjectURL(a.href);
  }

  async function remove(fp: string) {
    if (!confirm(`Delete ${fp}?`)) return;
    try {
      const q = new URLSearchParams({ machine, user, path: fp });
      await api(`/files?${q}`, { method: "DELETE" });
      toast("Deleted");
      void load(path);
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function mkdir() {
    const name = prompt("Folder name");
    if (!name) return;
    try {
      await api("/files/mkdir", {
        method: "POST",
        body: JSON.stringify({ machine, path: joinPath(path, name), user }),
      });
      toast("Folder created");
      void load(path);
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function upload(file: File) {
    const fd = new FormData();
    fd.append("machine", machine);
    fd.append("path", joinPath(path, file.name));
    fd.append("user", user);
    fd.append("file", file);
    try {
      await api("/files/upload", { method: "POST", body: fd });
      toast(`Uploaded ${file.name}`);
      void load(path);
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  const processed = useMemo(() => {
    return table.process(
      entries,
      (e, key) => {
        if (key === "name") return (e.is_dir ? "0" : "1") + e.name.toLowerCase();
        if (key === "size") return e.is_dir ? -1 : e.size ?? 0;
        if (key === "mtime") return e.mtime ?? 0;
        return "";
      },
      (e) => `${e.name} ${e.path}`,
    );
  }, [entries, table]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Files</h1>
          <p>Browse and move files on a connected host.</p>
        </div>
      </div>
      <div className="card">
        <div className="term-toolbar">
          <div>
            <label className="field" htmlFor="files-machine">Machine</label>
            <select id="files-machine" value={machine} onChange={(e) => setMachine(e.target.value)}>
              {(machinesQ.data || []).map((m) => (
                <option key={m.id} value={m.name}>
                  {m.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="field" htmlFor="files-remote-user">Remote user</label>
            <input id="files-remote-user" list="remoteUserListFiles" value={user} onChange={(e) => setUser(e.target.value)} />
            <datalist id="remoteUserListFiles">
              {REMOTE_USERS.map((u) => (
                <option key={u} value={u}>
                  {u}
                </option>
              ))}
            </datalist>
          </div>
          <button type="button" className="btn secondary" onClick={() => void load(path)} style={{ alignSelf: "end" }}>
            Refresh
          </button>
        </div>
        <div className="path-bar">
          <button type="button" className="btn secondary sm" onClick={() => void load(parentPath(path))} disabled={path === "/"}>
            ↑ Up
          </button>
          <input
            aria-label="Path"
            value={pathInput}
            onChange={(e) => setPathInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && void load(pathInput)}
          />
          <button type="button" className="btn sm" onClick={() => void load(pathInput)}>
            Go
          </button>
          <button type="button" className="btn secondary sm" onClick={() => void mkdir()}>
            New folder
          </button>
          <label className="btn secondary sm" style={{ cursor: "pointer", margin: 0 }}>
            Upload
            <input
              type="file"
              hidden
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void upload(f);
                e.target.value = "";
              }}
            />
          </label>
        </div>
        {err ? <div className="err">{err}</div> : null}
        {loading ? <p className="muted">Loading…</p> : null}
        <TableToolbar query={table.query} onQuery={table.setQuery} placeholder="Filter files…" />
        <section className="table-scroll" tabIndex={0} aria-label="Files">
          <table>
            <thead>
              <tr>
                <SortTh label="Name" col="name" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Size" col="size" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <SortTh label="Modified" col="mtime" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
                <th><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {processed.rows.length === 0 && !loading ? (
                <tr>
                  <td colSpan={4} className="empty">
                    Empty directory.
                  </td>
                </tr>
              ) : null}
              {processed.rows.map((ent) => (
                <tr key={ent.path}>
                  <td>
                    {ent.is_dir ? (
                      <button type="button" className="file-row-name dir" onClick={() => void load(ent.path)}>
                        {ent.name}/
                      </button>
                    ) : (
                      <span className="mono">{ent.name}</span>
                    )}
                  </td>
                  <td className="mono">{ent.is_dir ? "—" : fmtSize(ent.size)}</td>
                  <td className="mono">{ent.mtime ? fmtTime(new Date(ent.mtime * 1000).toISOString()) : "—"}</td>
                  <td className="row">
                    {!ent.is_dir ? (
                      <button type="button" className="btn secondary sm" onClick={() => void download(ent.path)}>
                        Download
                      </button>
                    ) : null}
                    <button type="button" className="btn danger sm" onClick={() => void remove(ent.path)}>
                      Delete
                    </button>
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
