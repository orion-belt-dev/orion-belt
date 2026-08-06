import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useToast } from "./Toast";

type TemplateEntry = {
  event_type: string;
  title: string;
  body: string;
  customized?: boolean;
  default_title: string;
  default_body: string;
  placeholders?: string[];
};

type TemplatesResponse = {
  templates?: TemplateEntry[];
};

type Draft = { title: string; body: string };

/**
 * Admin editor for the wording of outgoing notifications. Each event type has
 * built-in copy that an operator can override — to adjust tone, add context, or
 * match an org's conventions — without a code change or a restart, since the
 * server reads templates per delivery.
 *
 * Admin-only — the endpoints sit under /admin, so this renders nothing for a
 * non-admin rather than showing controls whose save would 403.
 */
export function NotificationTemplatesCard() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const q = useQuery({
    queryKey: ["notification-templates"],
    queryFn: () => api<TemplatesResponse>("/admin/notifications/templates"),
    retry: false,
  });

  const templates = q.data?.templates ?? [];

  // Drafts are keyed by event so an in-progress edit to one event survives a
  // save or reset of another.
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    if (!q.data?.templates) return;
    const next: Record<string, Draft> = {};
    for (const t of q.data.templates) next[t.event_type] = { title: t.title, body: t.body };
    setDrafts(next);
  }, [q.data]);

  if (q.isError) return null; // non-admin, or templates endpoint unavailable

  function edit(event: string, patch: Partial<Draft>) {
    setDrafts((prev) => ({ ...prev, [event]: { ...prev[event], ...patch } }));
  }

  function isDirty(t: TemplateEntry): boolean {
    const draft = drafts[t.event_type];
    if (!draft) return false;
    return draft.title !== t.title || draft.body !== t.body;
  }

  async function save(event: string) {
    const draft = drafts[event];
    if (!draft) return;
    setBusy(event);
    try {
      await api(`/admin/notifications/templates/${encodeURIComponent(event)}`, {
        method: "PUT",
        body: JSON.stringify({ title: draft.title, body: draft.body }),
      });
      toast("Notification template saved");
      void qc.invalidateQueries({ queryKey: ["notification-templates"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    } finally {
      setBusy(null);
    }
  }

  async function reset(event: string) {
    setBusy(event);
    try {
      await api(`/admin/notifications/templates/${encodeURIComponent(event)}`, { method: "DELETE" });
      toast("Reverted to the default wording");
      void qc.invalidateQueries({ queryKey: ["notification-templates"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="card" style={{ marginBottom: "0.85rem" }}>
      <h3>Notification templates</h3>
      <p className="muted">
        Wording for the notifications this server sends. Changes apply to the next notification delivered — no restart
        needed. Use the listed variables to insert details of the event.
      </p>

      {q.isLoading ? (
        <p className="muted">Loading…</p>
      ) : templates.length === 0 ? (
        <p className="muted">No templatable event types reported by the server.</p>
      ) : (
        templates.map((t) => {
          const draft = drafts[t.event_type] ?? { title: t.title, body: t.body };
          const dirty = isDirty(t);
          const pending = busy === t.event_type;

          return (
            <div
              key={t.event_type}
              style={{ marginTop: "1rem", paddingTop: "0.85rem", borderTop: "1px solid var(--line)" }}
            >
              <div className="row" style={{ alignItems: "center", gap: "0.5rem", flexWrap: "wrap" }}>
                <code>{t.event_type}</code>
                {t.customized ? <span className="muted">· customized</span> : null}
              </div>

              <label className="field" htmlFor={`tpl-title-${t.event_type}`} style={{ marginTop: "0.5rem" }}>
                Title
              </label>
              <input
                id={`tpl-title-${t.event_type}`}
                type="text"
                value={draft.title}
                disabled={pending}
                onChange={(e) => edit(t.event_type, { title: e.target.value })}
              />

              <label className="field" htmlFor={`tpl-body-${t.event_type}`} style={{ marginTop: "0.5rem" }}>
                Body
              </label>
              <textarea
                id={`tpl-body-${t.event_type}`}
                rows={3}
                value={draft.body}
                disabled={pending}
                onChange={(e) => edit(t.event_type, { body: e.target.value })}
                style={{ width: "100%" }}
              />

              {t.placeholders && t.placeholders.length > 0 ? (
                <p className="muted" style={{ marginTop: "0.35rem" }}>
                  Variables:{" "}
                  {t.placeholders.map((p, i) => (
                    <span key={p}>
                      {i > 0 ? ", " : ""}
                      <code>{`{{${p}}}`}</code>
                    </span>
                  ))}
                </p>
              ) : null}

              <div className="row" style={{ gap: "0.5rem", marginTop: "0.5rem" }}>
                <button className="btn sm" type="button" disabled={pending || !dirty} onClick={() => void save(t.event_type)}>
                  {pending ? "Saving…" : "Save"}
                </button>
                <button
                  className="btn sm secondary"
                  type="button"
                  disabled={pending || !t.customized}
                  title={t.customized ? "Revert to the built-in wording" : "Already using the built-in wording"}
                  onClick={() => void reset(t.event_type)}
                >
                  Reset to default
                </button>
              </div>
            </div>
          );
        })
      )}
    </div>
  );
}
