import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useToast } from "./Toast";

type Policy = {
  allowed_channels?: string[];
  mandatory_events?: Record<string, string[]>;
};

type PolicyResponse = {
  policy?: Policy;
  known_channels?: string[];
  known_events?: string[];
};

const CHANNEL_LABELS: Record<string, string> = {
  in_app: "In-app",
  email: "Email",
};

function channelLabel(channel: string): string {
  return CHANNEL_LABELS[channel] ?? channel;
}

/**
 * Admin editor for the boundary that per-user notification preferences are
 * resolved within: which channels users may choose, and which events are
 * delivered regardless of preference.
 *
 * Admin-only — the endpoints sit under /admin, so this renders nothing for a
 * non-admin rather than showing controls whose save would 403.
 */
export function NotificationPolicyCard() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const q = useQuery({
    queryKey: ["notification-policy"],
    queryFn: () => api<PolicyResponse>("/admin/notifications/policy"),
    retry: false,
  });

  const knownChannels = q.data?.known_channels ?? ["in_app", "email"];
  const knownEvents = q.data?.known_events ?? [];

  const [allowed, setAllowed] = useState<string[]>([]);
  const [mandatory, setMandatory] = useState<Record<string, string[]>>({});
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!q.data?.policy) return;
    // An empty allowed_channels list means "all channels" on the wire; expand
    // it here so the checkboxes show the effective state rather than blank.
    const stored = q.data.policy.allowed_channels ?? [];
    setAllowed(stored.length === 0 ? knownChannels : stored);
    setMandatory(q.data.policy.mandatory_events ?? {});
    // knownChannels is derived from the same response, so q.data drives this.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q.data]);

  if (q.isError) return null; // non-admin, or policy endpoint unavailable

  function toggleAllowed(channel: string, on: boolean) {
    setAllowed((prev) => (on ? [...new Set([...prev, channel])] : prev.filter((c) => c !== channel)));
  }

  function toggleMandatory(event: string, channel: string, on: boolean) {
    setMandatory((prev) => {
      const current = prev[event] ?? [];
      const next = on ? [...new Set([...current, channel])] : current.filter((c) => c !== channel);
      const out = { ...prev };
      if (next.length === 0) delete out[event];
      else out[event] = next;
      return out;
    });
  }

  async function save() {
    setSaving(true);
    try {
      await api("/admin/notifications/policy", {
        method: "PUT",
        body: JSON.stringify({ allowed_channels: allowed, mandatory_events: mandatory }),
      });
      toast("Notification policy saved");
      void qc.invalidateQueries({ queryKey: ["notification-policy"] });
      // User-facing preferences are rendered against these bounds, so drop
      // the cached copy too.
      void qc.invalidateQueries({ queryKey: ["notification-prefs"] });
    } catch (ex) {
      toast(ex instanceof Error ? ex.message : String(ex), "err");
    } finally {
      setSaving(false);
    }
  }

  const mandatoryChannels = new Set(Object.values(mandatory).flat());

  return (
    <div className="card" style={{ marginBottom: "0.85rem" }}>
      <h3>Notification policy</h3>
      <p className="muted">
        Bounds for what users may choose in their own notification preferences. Leaving every channel selectable and
        nothing required matches the default behaviour.
      </p>

      {q.isLoading ? (
        <p className="muted">Loading…</p>
      ) : (
        <>
          <div style={{ marginTop: "0.75rem" }}>
            <label className="field">Channels users may enable</label>
            <div className="row" style={{ gap: "1rem", flexWrap: "wrap" }}>
              {knownChannels.map((channel) => {
                const required = mandatoryChannels.has(channel);
                return (
                  <label
                    key={channel}
                    className="row"
                    style={{ alignItems: "center", gap: "0.4rem" }}
                    title={required ? "Required by a mandatory event below" : undefined}
                  >
                    <input
                      type="checkbox"
                      checked={allowed.includes(channel) || required}
                      disabled={required}
                      onChange={(e) => toggleAllowed(channel, e.target.checked)}
                    />
                    {channelLabel(channel)}
                  </label>
                );
              })}
            </div>
          </div>

          <div style={{ marginTop: "1rem" }}>
            <label className="field">Always deliver (users cannot turn these off)</label>
            {knownEvents.length === 0 ? (
              <p className="muted">No event types reported by the server.</p>
            ) : (
              <div style={{ overflowX: "auto" }}>
                <table>
                  <thead>
                    <tr>
                      <th>Event</th>
                      {knownChannels.map((channel) => (
                        <th key={channel}>{channelLabel(channel)}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {knownEvents.map((event) => (
                      <tr key={event}>
                        <td>
                          <code>{event}</code>
                        </td>
                        {knownChannels.map((channel) => (
                          <td key={channel}>
                            <input
                              type="checkbox"
                              aria-label={`${event} always delivered via ${channelLabel(channel)}`}
                              checked={(mandatory[event] ?? []).includes(channel)}
                              onChange={(e) => toggleMandatory(event, channel, e.target.checked)}
                            />
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          <button className="btn sm" type="button" disabled={saving} onClick={() => void save()} style={{ marginTop: "0.75rem" }}>
            {saving ? "Saving…" : "Save policy"}
          </button>
        </>
      )}
    </div>
  );
}
