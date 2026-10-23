import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AccessExplanation } from "../lib/types";
import { fmtTime } from "../lib/format";

// AccessExplanationPanel shows why the signed-in user can or cannot reach
// machine as remoteUser: the gateway's decision and the relationships behind it.
export function AccessExplanationPanel({ machine, remoteUser }: { machine: string; remoteUser: string }) {
  const params = new URLSearchParams({ machine, remote_user: remoteUser });
  const ex = useQuery({
    queryKey: ["explain", machine, remoteUser],
    queryFn: () => api<AccessExplanation>(`/access/explain?${params}`),
  });

  if (ex.isLoading) return <div className="muted">Checking…</div>;
  if (ex.error) return <div className="err">{ex.error instanceof Error ? ex.error.message : String(ex.error)}</div>;
  if (!ex.data) return null;

  return (
    <div>
      <p className={ex.data.allowed ? "okmsg" : "err"}>{ex.data.summary}</p>
      <ul className="muted">
        {ex.data.chain.map((link, i) => (
          <li key={`${link.ref || link.relation}-${i}`}>
            {link.holds ? "✓ " : ""}
            <span className="mono">{link.ref || link.relation}</span>: {link.detail}
            {link.reason ? ` (reason: ${link.reason})` : ""}
            {link.via ? ` via ${link.via}` : ""}
            {link.at ? ` · ${fmtTime(link.at)}` : ""}
          </li>
        ))}
      </ul>
    </div>
  );
}
