import { useState } from "react";
import { apiDownload } from "../lib/api";

type ReportName = "soc2-access" | "iso27001-access-review" | "pci-admin-activity";
type Format = "csv" | "pdf" | "json";

const REPORTS: { name: ReportName; label: string; hint: string }[] = [
  {
    name: "soc2-access",
    label: "Access report (SOC 2)",
    hint: "Every session and access request in the range, with reason and approver.",
  },
  {
    name: "iso27001-access-review",
    label: "Access review (ISO 27001)",
    hint: "Grants in force at a point in time, standing vs time-boxed.",
  },
  {
    name: "pci-admin-activity",
    label: "Admin activity (PCI)",
    hint: "Sessions on machines tagged pci: in-scope.",
  },
];

function isoDay(offsetDays: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  return d.toISOString().slice(0, 10);
}

// ComplianceReportsCard exports the organisation-wide compliance reports.
// Render it only for admins, operators and auditors; the API refuses others.
export function ComplianceReportsCard() {
  const [report, setReport] = useState<ReportName>("soc2-access");
  const [from, setFrom] = useState(isoDay(-30));
  const [to, setTo] = useState(isoDay(0));
  const [asOf, setAsOf] = useState(isoDay(0));
  const [err, setErr] = useState("");

  const current = REPORTS.find((r) => r.name === report)!;
  const pointInTime = report === "iso27001-access-review";

  async function download(format: Format) {
    setErr("");
    const params = new URLSearchParams({ format });
    if (pointInTime) {
      // A bare date means the end of that day for the review.
      params.set("as_of", `${asOf}T23:59:59Z`);
    } else {
      params.set("from", from);
      params.set("to", to);
    }
    try {
      await apiDownload(`/reports/${report}/export?${params.toString()}`, `${report}.${format}`);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="card">
      <h3>Compliance reports</h3>
      <p className="muted">{current.hint}</p>
      {err ? <div className="err">{err}</div> : null}
      <div className="form-grid">
        <div>
          <label className="field" htmlFor="compliance-report">
            Report
          </label>
          <select id="compliance-report" value={report} onChange={(e) => setReport(e.target.value as ReportName)}>
            {REPORTS.map((r) => (
              <option key={r.name} value={r.name}>
                {r.label}
              </option>
            ))}
          </select>
        </div>
        {pointInTime ? (
          <div>
            <label className="field" htmlFor="compliance-as-of">
              As of
            </label>
            <input id="compliance-as-of" type="date" value={asOf} onChange={(e) => setAsOf(e.target.value)} />
          </div>
        ) : (
          <>
            <div>
              <label className="field" htmlFor="compliance-from">
                From
              </label>
              <input id="compliance-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
            </div>
            <div>
              <label className="field" htmlFor="compliance-to">
                To
              </label>
              <input id="compliance-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} />
            </div>
          </>
        )}
      </div>
      <div className="row" style={{ marginTop: "0.75rem" }}>
        <button type="button" className="btn secondary sm" onClick={() => void download("csv")}>
          Export CSV
        </button>
        <button type="button" className="btn secondary sm" onClick={() => void download("pdf")}>
          Export PDF
        </button>
        <button type="button" className="btn secondary sm" onClick={() => void download("json")}>
          Export JSON
        </button>
      </div>
    </div>
  );
}
