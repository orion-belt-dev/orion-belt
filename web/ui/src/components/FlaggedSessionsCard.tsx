import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AccessAnomaly } from "../lib/types";
import { fmtTime, shortId } from "../lib/format";

const KIND_LABEL: Record<string, string> = {
  first_time_access: "First access",
  off_hours: "Off hours",
};

// FlaggedSessionsCard lists sessions flagged as unusual. Flags are advisory:
// the sessions ran, and each row says which signal raised it. Render it only
// for admins, operators and auditors; the API refuses others.
export function FlaggedSessionsCard({ userName }: { userName: (id?: string) => string }) {
  const flagged = useQuery({
    queryKey: ["anomalies"],
    queryFn: () => api<AccessAnomaly[]>("/anomalies?limit=50"),
  });

  return (
    <div className="card">
      <h3>Flagged sessions</h3>
      <p className="muted">Unusual access for review. These sessions were allowed, not blocked.</p>
      {flagged.error ? <div className="err">{String(flagged.error)}</div> : null}
      {flagged.data && flagged.data.length === 0 ? <div className="empty">Nothing flagged.</div> : null}
      {flagged.data && flagged.data.length > 0 ? (
        <table>
          <thead>
            <tr>
              <th>Flagged</th>
              <th>Kind</th>
              <th>User</th>
              <th>Target</th>
              <th>Why</th>
              <th>Session</th>
            </tr>
          </thead>
          <tbody>
            {flagged.data.map((a) => (
              <tr key={a.id}>
                <td className="mono">{fmtTime(a.flagged_at)}</td>
                <td>{KIND_LABEL[a.kind] || a.kind}</td>
                <td>{userName(a.user_id)}</td>
                <td className="mono">
                  {a.remote_user}@{a.machine}
                </td>
                <td>{a.signal}</td>
                <td className="mono">{shortId(a.session_id)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
    </div>
  );
}
