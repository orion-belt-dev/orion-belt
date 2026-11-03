import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { getJwt, getSessionToken } from "../lib/api";
import { shortId } from "../lib/format";

export function LiveSessionWatch({ sessionId, onClose }: { sessionId: string; onClose: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState("connecting…");

  useEffect(() => {
    if (!hostRef.current) return;
    const term = new Terminal({
      cursorBlink: false,
      disableStdin: true,
      convertEol: true,
      fontFamily: "JetBrains Mono, ui-monospace, monospace",
      fontSize: 13,
      theme: { background: "#070d0f", foreground: "#e8f0f2", cursor: "#e8a54b" },
      cols: 120,
      rows: 40,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(hostRef.current);
    try {
      fit.fit();
    } catch {
      /* ignore */
    }

    const proto = location.protocol === "https:" ? "wss" : "ws";
    const q = new URLSearchParams();
    const session = getSessionToken();
    const jwt = getJwt();
    if (session) q.set("token", session);
    if (jwt) q.set("access_token", jwt);
    const ws = new WebSocket(`${proto}://${location.host}/api/v1/sessions/${encodeURIComponent(sessionId)}/watch?${q}`);
    ws.binaryType = "arraybuffer";
    ws.addEventListener("open", () => setStatus("live"));
    ws.addEventListener("message", (ev) => {
      if (typeof ev.data === "string") {
        term.write(ev.data);
        return;
      }
      if (ev.data instanceof Blob) {
        void ev.data.arrayBuffer().then((buf) => term.write(new Uint8Array(buf)));
        return;
      }
      term.write(new Uint8Array(ev.data as ArrayBuffer));
    });
    ws.addEventListener("error", () => setStatus("error"));
    ws.addEventListener("close", () => setStatus("ended"));

    const onResize = () => {
      try {
        fit.fit();
      } catch {
        /* ignore */
      }
    };
    window.addEventListener("resize", onResize);

    return () => {
      window.removeEventListener("resize", onResize);
      ws.close();
      term.dispose();
    };
  }, [sessionId]);

  return (
    <div className="card" style={{ marginTop: "1rem" }} id="watch-panel">
      <div className="row" style={{ justifyContent: "space-between", marginBottom: "0.5rem" }}>
        <h3 style={{ margin: 0 }}>
          Live watch · <span className="mono">{shortId(sessionId)}</span> · {status}
        </h3>
        <button type="button" className="btn secondary sm" onClick={onClose}>
          Stop
        </button>
      </div>
      <div ref={hostRef} style={{ minHeight: "20rem" }} />
    </div>
  );
}
