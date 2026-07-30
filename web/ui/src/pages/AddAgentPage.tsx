import { useState } from "react";
import type { FormEvent } from "react";
import { api } from "../lib/api";
import { useToast } from "../components/Toast";
import { useAuth } from "../auth/AuthContext";

import logoDebian from "../assets/distros/debian.png";
import logoUbuntu from "../assets/distros/ubuntu.png";
import logoRhel from "../assets/distros/rhel.png";
import logoRocky from "../assets/distros/rocky.png";
import logoFedora from "../assets/distros/fedora.png";
import logoSuse from "../assets/distros/suse.png";
import logoAlpine from "../assets/distros/alpine.png";
import logoLinux from "../assets/distros/linux.png";

/** Public GitHub Pages mirror (orion-belt-dev/packages). Local: make serve-packages → :8765 */
const DEFAULT_PACKAGE_BASE_URL = "https://orion-belt-dev.github.io/packages";
/** Matches the seeded Pages release when the gateway build is untagged/dev. */
const DEFAULT_PACKAGE_VERSION = "1.0.0";

const OS_OPTIONS = [
  {
    id: "debian",
    label: "Debian / Ubuntu",
    hint: "deb + systemd",
    logos: [logoDebian, logoUbuntu],
    versions: "Debian 11–13 · Ubuntu 20.04 / 22.04 / 24.04 (amd64, arm64)",
  },
  {
    id: "rhel",
    label: "RHEL / Rocky / Fedora",
    hint: "rpm + systemd",
    logos: [logoRhel, logoRocky, logoFedora],
    versions: "RHEL/Rocky 8–10 · Fedora 39+ (amd64, arm64)",
  },
  {
    id: "suse",
    label: "openSUSE",
    hint: "rpm via zypper",
    logos: [logoSuse],
    versions: "Leap 15.5+ · Tumbleweed (amd64)",
  },
  {
    id: "alpine",
    label: "Alpine",
    hint: "apk or binary",
    logos: [logoAlpine],
    versions: "Alpine 3.18+ (amd64, arm64)",
  },
  {
    id: "linux",
    label: "Generic Linux",
    hint: "raw binary",
    logos: [logoLinux],
    versions: "Any glibc/musl Linux with systemd or nohup (amd64, arm64)",
  },
];

type ScriptResp = {
  script: string;
  filename: string;
  message?: string;
  machine_id?: string;
};

export function AddAgentPage() {
  const { version } = useAuth();
  const { toast } = useToast();
  const [os, setOs] = useState("debian");
  const [name, setName] = useState("");
  const [hostname, setHostname] = useState("");
  const [port, setPort] = useState(22);
  const [env, setEnv] = useState("production");
  const [gw, setGw] = useState(window.location.hostname || "127.0.0.1");
  const [gwPort, setGwPort] = useState(2222);
  const [pkg, setPkg] = useState(DEFAULT_PACKAGE_BASE_URL);
  const [ver, setVer] = useState(() => {
    let v = version?.version || version?.display || DEFAULT_PACKAGE_VERSION;
    if (String(v).startsWith("v")) v = String(v).slice(1);
    if (v === "dev" || v === "0.0.0") v = DEFAULT_PACKAGE_VERSION;
    return String(v);
  });
  const [script, setScript] = useState("");
  const [filename, setFilename] = useState("orion-belt-install-agent.sh");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const selected = OS_OPTIONS.find((o) => o.id === os) || OS_OPTIONS[0];

  async function onGenerate(e: FormEvent) {
    e.preventDefault();
    setErr("");
    if (!name.trim()) {
      setErr("Agent name is required");
      return;
    }
    setBusy(true);
    try {
      const res = await api<ScriptResp>("/admin/agents/install-script", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          hostname: hostname.trim() || name.trim(),
          port,
          os,
          gateway_host: gw.trim(),
          gateway_port: gwPort,
          package_base_url: pkg.trim(),
          version: ver.trim(),
          tags: { os, environment: env.trim() || "production" },
        }),
      });
      setScript(res.script || "");
      setFilename(res.filename || `orion-belt-install-${name.trim()}.sh`);
      setMsg(res.message || "Script ready.");
      toast("Agent registered — run the script on the host");
    } catch (ex) {
      setErr(ex instanceof Error ? ex.message : String(ex));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Add agent</h1>
          <p>Generate a one-shot install script for a new host.</p>
        </div>
      </div>
      <form className="card" onSubmit={onGenerate}>
        <h3>Target</h3>
        <div className="form-grid">
          <div>
            <label className="field">Agent name</label>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="web-01" />
          </div>
          <div>
            <label className="field">Hostname</label>
            <input value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="defaults to agent name" />
          </div>
          <div>
            <label className="field">Host SSH port</label>
            <input type="number" value={port} onChange={(e) => setPort(Number(e.target.value) || 22)} />
          </div>
          <div>
            <label className="field">Environment tag</label>
            <input value={env} onChange={(e) => setEnv(e.target.value)} />
          </div>
        </div>
        <label className="field" style={{ marginTop: "0.85rem" }}>
          Operating system
        </label>
        <div className="os-picker">
          {OS_OPTIONS.map((o) => (
            <button key={o.id} type="button" className={`os-card${os === o.id ? " active" : ""}`} onClick={() => setOs(o.id)}>
              <span className="os-logos">
                {o.logos.map((src, i) => (
                  <img key={i} src={src} alt="" width={28} height={28} />
                ))}
              </span>
              <strong>{o.label}</strong>
              <span className="muted">{o.hint}</span>
            </button>
          ))}
        </div>
        <p className="muted os-versions">
          Supported: <strong>{selected.versions}</strong>
        </p>
        <h3 style={{ marginTop: "1.25rem" }}>Connection &amp; packages</h3>
        <div className="form-grid">
          <div>
            <label className="field">Gateway host</label>
            <input value={gw} onChange={(e) => setGw(e.target.value)} />
          </div>
          <div>
            <label className="field">Gateway SSH port</label>
            <input type="number" value={gwPort} onChange={(e) => setGwPort(Number(e.target.value) || 2222)} />
          </div>
          <div>
            <label className="field">Package base URL</label>
            <input
              value={pkg}
              onChange={(e) => setPkg(e.target.value)}
              placeholder={DEFAULT_PACKAGE_BASE_URL}
            />
          </div>
          <div>
            <label className="field">Package version</label>
            <input value={ver} onChange={(e) => setVer(e.target.value)} />
          </div>
        </div>
        {err ? <div className="err">{err}</div> : null}
        <div className="row" style={{ marginTop: "1rem" }}>
          <button className="btn" type="submit" disabled={busy}>
            Generate install script
          </button>
        </div>
      </form>
      {script ? (
        <div className="card" style={{ marginTop: "1rem" }}>
          <div className="row" style={{ justifyContent: "space-between" }}>
            <h3 style={{ margin: 0 }}>Install script</h3>
            <div className="row">
              <button
                type="button"
                className="btn secondary sm"
                onClick={async () => {
                  await navigator.clipboard.writeText(script);
                  toast("Script copied");
                }}
              >
                Copy
              </button>
              <a className="btn secondary sm" href={`data:text/x-shellscript;charset=utf-8,${encodeURIComponent(script)}`} download={filename}>
                Download
              </a>
            </div>
          </div>
          <p className="muted">{msg}</p>
          <pre className="code">{script}</pre>
        </div>
      ) : null}
    </>
  );
}
