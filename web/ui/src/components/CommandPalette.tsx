import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useRole } from "../auth/AuthContext";
import { api } from "../lib/api";
import { NAV } from "../lib/nav";
import type { Machine, Session } from "../lib/types";
import { shortId } from "../lib/format";

type Item = { id: string; label: string; hint?: string; path: string; group: string };

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const navigate = useNavigate();
  const role = useRole();

  const machines = useQuery({
    queryKey: ["machines"],
    queryFn: () => api<Machine[]>("/machines"),
    enabled: open,
  });
  const sessions = useQuery({
    queryKey: ["sessions", "palette"],
    queryFn: () => api<Session[]>("/sessions/active"),
    enabled: open,
  });

  const items = useMemo(() => {
    const navItems: Item[] = (NAV[role] || NAV.user).map((n) => ({
      id: `nav-${n.id}`,
      label: n.label,
      hint: n.path,
      path: n.path,
      group: "Navigate",
    }));
    const machineItems: Item[] = (machines.data || []).map((m) => ({
      id: `machine-${m.id}`,
      label: m.name,
      hint: `${m.hostname}:${m.port}`,
      path: `/terminal?machine=${encodeURIComponent(m.name)}`,
      group: "Machines → Terminal",
    }));
    const sessionItems: Item[] = (sessions.data || []).slice(0, 12).map((s) => ({
      id: `session-${s.id}`,
      label: `Session ${shortId(s.id)}`,
      hint: s.status || "active",
      path: `/sessions`,
      group: "Active sessions",
    }));
    const all = [...navItems, ...machineItems, ...sessionItems];
    const needle = q.trim().toLowerCase();
    if (!needle) return all;
    return all.filter(
      (it) =>
        it.label.toLowerCase().includes(needle) ||
        (it.hint || "").toLowerCase().includes(needle) ||
        it.group.toLowerCase().includes(needle),
    );
  }, [role, machines.data, sessions.data, q]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const meta = e.metaKey || e.ctrlKey;
      if (meta && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((v) => !v);
        setQ("");
        setActive(0);
      } else if (e.key === "Escape") {
        setOpen(false);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (open) {
      const t = window.setTimeout(() => inputRef.current?.focus(), 20);
      return () => window.clearTimeout(t);
    }
  }, [open]);

  // A modal <dialog> traps focus and makes the page behind it inert. A press
  // outside the box lands on the dialog itself (its backdrop) and closes it,
  // as Esc does from the keyboard.
  useEffect(() => {
    const dialog = dialogRef.current;
    if (!open || !dialog) return;
    if (!dialog.open) dialog.showModal();
    const onPress = (e: MouseEvent) => {
      if (e.target === dialog) setOpen(false);
    };
    dialog.addEventListener("mousedown", onPress);
    return () => dialog.removeEventListener("mousedown", onPress);
  }, [open]);

  function go(item: Item) {
    setOpen(false);
    navigate(item.path);
  }

  if (!open) return null;

  const grouped = items.reduce<Record<string, Item[]>>((acc, it) => {
    (acc[it.group] ||= []).push(it);
    return acc;
  }, {});
  const flat = items;

  return (
    <dialog
      ref={dialogRef}
      className="palette card"
      aria-label="Command palette"
      onClose={() => setOpen(false)}
    >
      <div className="palette-body">
        <input
          ref={inputRef}
          className="palette-input"
          placeholder="Jump to page, machine, or session… (Esc to close)"
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setActive(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setActive((i) => Math.min(i + 1, flat.length - 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setActive((i) => Math.max(i - 1, 0));
            } else if (e.key === "Enter" && flat[active]) {
              e.preventDefault();
              go(flat[active]);
            }
          }}
        />
        <div className="palette-list">
          {flat.length === 0 ? <div className="empty">No matches.</div> : null}
          {Object.entries(grouped).map(([group, list]) => (
            <div key={group}>
              <div className="palette-group">{group}</div>
              {list.map((it) => {
                const idx = flat.indexOf(it);
                return (
                  <button
                    key={it.id}
                    type="button"
                    className={`palette-item${idx === active ? " active" : ""}`}
                    onMouseEnter={() => setActive(idx)}
                    onClick={() => go(it)}
                  >
                    <span>{it.label}</span>
                    {it.hint ? <span className="muted mono">{it.hint}</span> : null}
                  </button>
                );
              })}
            </div>
          ))}
        </div>
        <div className="palette-foot muted">
          <span>↑↓ move</span>
          <span>↵ open</span>
          <span>esc close</span>
        </div>
      </div>
    </dialog>
  );
}
