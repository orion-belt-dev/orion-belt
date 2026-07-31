import { useMemo, useState } from "react";
import type { FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { User } from "../lib/types";
import { Badge } from "../components/Badge";
import { useAuth } from "../auth/AuthContext";
import { canApprove } from "../lib/nav";
import { useToast } from "../components/Toast";
import { Pagination, SortTh, TableToolbar, useTableState } from "../components/DataTable";
import { shortId } from "../lib/format";

type Draft = { email: string; role: string; public_key: string };

export function UsersPage() {
  const { user } = useAuth();
  const manage = canApprove(user);
  const { toast } = useToast();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["users"], queryFn: () => api<User[]>("/users") });
  const table = useTableState<User>({ pageSize: 20 });
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("user");
  const [pubkey, setPubkey] = useState("");
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});

  const create = useMutation({
    mutationFn: () =>
      api("/admin/users", {
        method: "POST",
        body: JSON.stringify({
          username,
          email,
          role,
          public_key: pubkey || undefined,
          is_admin: role === "admin",
        }),
      }),
    onSuccess: () => {
      toast("User created");
      setUsername("");
      setEmail("");
      setPubkey("");
      void qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e: Error) => toast(e.message, "err"),
  });

  function onCreate(e: FormEvent) {
    e.preventDefault();
    create.mutate();
  }

  function draftFor(u: User): Draft {
    if (drafts[u.id]) return drafts[u.id];
    return {
      email: u.email || "",
      role: u.role || (u.is_admin ? "admin" : "user"),
      public_key: u.public_key || "",
    };
  }

  function setDraft(id: string, patch: Partial<Draft>) {
    const u = (q.data || []).find((x) => x.id === id);
    if (!u) return;
    setDrafts((prev) => ({ ...prev, [id]: { ...draftFor(u), ...prev[id], ...patch } }));
  }

  async function saveUser(id: string) {
    const u = (q.data || []).find((x) => x.id === id);
    if (!u) return;
    const d = draftFor(u);
    try {
      await api(`/admin/users/${encodeURIComponent(id)}`, {
        method: "PUT",
        body: JSON.stringify({
          email: d.email,
          role: d.role,
          public_key: d.public_key,
          is_admin: d.role === "admin",
        }),
      });
      toast(`Saved ${u.username}`);
      setDrafts((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      void qc.invalidateQueries({ queryKey: ["users"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  async function deleteUser(id: string, name: string) {
    if (
      !confirm(
        `Permanently delete user ${name}?\n\nThis removes their permissions, access requests, session history, and credentials.\n\nAudit entries they produced are kept (with the user field cleared). There is no archive for users — cancel if you are unsure.`,
      )
    ) {
      return;
    }
    try {
      await api(`/admin/users/${encodeURIComponent(id)}`, { method: "DELETE" });
      toast("User deleted");
      void qc.invalidateQueries({ queryKey: ["users"] });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  }

  const processed = useMemo(() => {
    return table.process(
      q.data || [],
      (u, key) => {
        if (key === "username") return u.username;
        if (key === "email") return u.email;
        if (key === "role") return u.role || (u.is_admin ? "admin" : "user");
        if (key === "mfa") return u.mfa_enabled ? 1 : 0;
        return "";
      },
      (u) => `${u.username} ${u.email} ${u.role || ""} ${u.id}`,
    );
  }, [q.data, table]);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Users</h1>
          <p>People on this gateway{manage ? " — add or edit below." : "."}</p>
        </div>
      </div>
      {manage ? (
        <form className="card" onSubmit={onCreate} style={{ marginBottom: "1rem" }}>
          <h3>Create user</h3>
          <div className="form-grid">
            <div>
              <label className="field">Username</label>
              <input value={username} onChange={(e) => setUsername(e.target.value)} required />
            </div>
            <div>
              <label className="field">Email</label>
              <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
            </div>
            <div>
              <label className="field">Role</label>
              <select value={role} onChange={(e) => setRole(e.target.value)}>
                <option value="user">user</option>
                <option value="auditor">auditor</option>
                <option value="operator">operator</option>
                <option value="admin">admin</option>
              </select>
            </div>
          </div>
          <label className="field" style={{ marginTop: "0.75rem" }}>
            Public key (optional)
          </label>
          <textarea rows={3} value={pubkey} onChange={(e) => setPubkey(e.target.value)} />
          <button className="btn sm" type="submit" style={{ marginTop: "0.75rem" }}>
            Create
          </button>
        </form>
      ) : null}
      <div className="card">
        <TableToolbar query={table.query} onQuery={table.setQuery} placeholder="Filter users…" />
        <table>
          <thead>
            <tr>
              <SortTh label="Username" col="username" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="Email" col="email" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="Role" col="role" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <SortTh label="MFA" col="mfa" sortKey={table.sortKey} sortDir={table.sortDir} onSort={table.toggleSort} />
              <th>WebAuthn</th>
              {manage ? <th /> : null}
            </tr>
          </thead>
          <tbody>
            {processed.rows.map((u) => {
              const r = u.role || (u.is_admin ? "admin" : "user");
              const d = draftFor(u);
              return (
                <tr key={u.id}>
                  <td>
                    <strong>{u.username}</strong>
                    <div className="mono muted" style={{ fontSize: ".72rem" }}>
                      {shortId(u.id)}
                    </div>
                  </td>
                  <td>
                    {manage ? (
                      <input value={d.email} onChange={(e) => setDraft(u.id, { email: e.target.value })} style={{ minWidth: 160 }} />
                    ) : (
                      u.email
                    )}
                  </td>
                  <td>
                    {manage ? (
                      <select value={d.role} onChange={(e) => setDraft(u.id, { role: e.target.value })}>
                        {["user", "auditor", "operator", "admin"].map((opt) => (
                          <option key={opt} value={opt}>
                            {opt}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <Badge status={r}>{r}</Badge>
                    )}
                  </td>
                  <td>{u.mfa_enabled ? "on" : "off"}</td>
                  <td>{u.webauthn_enabled ? "on" : "off"}</td>
                  {manage ? (
                    <td className="row">
                      <button type="button" className="btn secondary sm" onClick={() => void saveUser(u.id)}>
                        Save
                      </button>
                      <button type="button" className="btn danger sm" onClick={() => void deleteUser(u.id, u.username)}>
                        Delete
                      </button>
                    </td>
                  ) : null}
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
      </div>
    </>
  );
}
