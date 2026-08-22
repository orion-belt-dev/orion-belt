import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { api, getJwt, getSessionToken } from "../lib/api";
import type { Machine } from "../lib/types";
import { useToast } from "../components/Toast";

const REMOTE_USERS = ["root", "ubuntu", "ec2-user", "admin", "deploy", "alpine", "opensuse", "debian"];

export function TerminalPage() {
  const { toast } = useToast();
  const [params] = useSearchParams();
  const machinesQ = useQuery({ queryKey: ["machines"], queryFn: () => api<Machine[]>("/machines") });
  // Deep links (access catalog, command palette) name the target up front.
  const [machine, setMachine] = useState(params.get("machine") || "");
  const [user, setUser] = useState(params.get("user") || "root");
  const [connected, setConnected] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [status, setStatus] = useState("offline");
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    const list = machinesQ.data;
    if (!list?.length) return;
    // Also covers a deep link naming a machine that no longer exists.
    if (machine && list.some((m) => m.name === machine)) return;
    setMachine(list[0].name);
  }, [machinesQ.data, machine]);

  useEffect(() => {
    return () => {
      wsRef.current?.close();
      termRef.current?.dispose();
    };
  }, []);

  function fitAndNotify() {
    const term = termRef.current;
    const fit = fitRef.current;
    if (!term || !fit) return;
    try {
      fit.fit();
      const dims = { type: "resize", cols: term.cols, rows: term.rows };
      if (wsRef.current?.readyState === WebSocket.OPEN) wsRef.current.send(JSON.stringify(dims));
    } catch {
      /* ignore */
    }
  }

  useEffect(() => {
    const onResize = () => fitAndNotify();
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  useEffect(() => {
    const t = window.setTimeout(() => fitAndNotify(), 50);
    return () => window.clearTimeout(t);
  }, [expanded]);

  useEffect(() => {
    if (!expanded) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setExpanded(false);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [expanded]);

  function disconnect() {
    wsRef.current?.close();
    wsRef.current = null;
    setConnected(false);
    setStatus("offline");
  }

  function ensureTerm() {
    if (!hostRef.current) return null;
    if (termRef.current) {
      fitAndNotify();
      return termRef.current;
    }
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: "JetBrains Mono, ui-monospace, monospace",
      fontSize: 13,
      theme: { background: "#070d0f", foreground: "#e8f0f2", cursor: "#e8a54b" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(hostRef.current);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;
    term.onData((data) => {
      if (wsRef.current?.readyState === WebSocket.OPEN) wsRef.current.send(data);
    });
    return term;
  }

  function connect() {
    if (!machine) {
      toast("Select a machine", "err");
      return;
    }
    const session = getSessionToken();
    const jwt = getJwt();
    if (!session && !jwt) {
      toast("Not signed in — refresh and log in again", "err");
      return;
    }
    disconnect();
    const term = ensureTerm();
    if (!term) {
      toast("Terminal host not ready", "err");
      return;
    }

    const proto = location.protocol === "https:" ? "wss" : "ws";
    const q = new URLSearchParams({ machine, user });
    // Prefer session ?token=; JWT as ?access_token= (middleware validates JWT properly).
    if (session) q.set("token", session);
    if (jwt) q.set("access_token", jwt);
    setStatus("connecting…");
    const ws = new WebSocket(`${proto}://${location.host}/api/v1/terminal/ws?${q}`);
    ws.binaryType = "arraybuffer";
    ws.onopen = () => {
      setConnected(true);
      setStatus("live");
      toast("Terminal connected");
      term.focus();
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    ws.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        if (ev.data.startsWith("error:")) {
          toast(ev.data, "err");
          setStatus(ev.data);
        } else term.write(ev.data);
        return;
      }
      if (ev.data instanceof Blob) {
        void ev.data.arrayBuffer().then((buf) => term.write(new Uint8Array(buf)));
        return;
      }
      term.write(new Uint8Array(ev.data as ArrayBuffer));
    };
    ws.onerror = () => {
      setStatus("error");
      toast("WebSocket error — check Vite proxy (ws) and gateway auth", "err");
    };
    ws.onclose = (ev) => {
      setConnected(false);
      wsRef.current = null;
      if (ev.code !== 1000) {
        setStatus(`closed (${ev.code})`);
        toast(`Terminal closed (${ev.code}${ev.reason ? `: ${ev.reason}` : ""})`, "err");
      } else {
        setStatus("offline");
      }
    };
    wsRef.current = ws;
  }

  return (
    <>
      {!expanded ? (
        <div className="page-head">
          <div>
            <h1>Terminal</h1>
            <p>Open a shell on a connected host. Sessions are recorded.</p>
          </div>
        </div>
      ) : null}
      <div className={`card term-card${expanded ? " term-expanded" : ""}`}>
        <div className="term-toolbar">
          <div>
            <label className="field">Machine</label>
            <select value={machine} onChange={(e) => setMachine(e.target.value)} disabled={connected}>
              {(machinesQ.data || []).map((m) => (
                <option key={m.id} value={m.name}>
                  {m.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="field">Remote user</label>
            <input list="termRemoteUsers" value={user} onChange={(e) => setUser(e.target.value)} disabled={connected} />
            <datalist id="termRemoteUsers">
              {REMOTE_USERS.map((u) => (
                <option key={u} value={u} />
              ))}
            </datalist>
          </div>
          <div className="row" style={{ alignSelf: "end" }}>
            <button type="button" className="btn" onClick={connect} disabled={connected}>
              Connect
            </button>
            <button type="button" className="btn secondary" onClick={disconnect} disabled={!connected}>
              Disconnect
            </button>
            <button type="button" className="btn secondary" onClick={() => setExpanded((v) => !v)}>
              {expanded ? "Exit expand" : "Expand"}
            </button>
            <span className="muted">{status}</span>
          </div>
        </div>
        <div className={`xterm-host${expanded ? " xterm-host-lg" : ""}`} ref={hostRef} />
      </div>
    </>
  );
}
