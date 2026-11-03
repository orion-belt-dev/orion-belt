import { useEffect, useMemo, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { shortId } from "../lib/format";

type CastEvent = { t: number; type: string; data: string };
type Cast = { header: { width?: number; height?: number; version?: number }; events: CastEvent[] };

function parseCast(text: string): Cast | null {
  const lines = text.split(/\r?\n/).filter((l) => l.trim().length);
  if (!lines.length) throw new Error("empty recording");
  const header = JSON.parse(lines[0]) as { version?: number; width?: number; height?: number };
  if (!header || header.version !== 2) return null;
  const events: CastEvent[] = [];
  for (let i = 1; i < lines.length; i++) {
    const ev = JSON.parse(lines[i]) as unknown[];
    if (!Array.isArray(ev) || ev.length < 3) continue;
    events.push({ t: Number(ev[0]) || 0, type: String(ev[1]), data: String(ev[2]) });
  }
  return { header, events };
}

function fmtCastTime(sec: number) {
  const s = Math.max(0, sec || 0);
  const m = Math.floor(s / 60);
  const r = s - m * 60;
  return `${m}:${r.toFixed(1).padStart(4, "0")}`;
}

export function CastPlayer({ sessionId, text }: { sessionId: string; text: string }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const cast = useMemo(() => {
    try {
      return parseCast(text);
    } catch {
      return null;
    }
  }, [text]);
  const [clock, setClock] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(1);
  const idxRef = useRef(0);
  const timerRef = useRef<number | null>(null);
  const duration = cast?.events.length ? cast.events[cast.events.length - 1].t : 0;

  useEffect(() => {
    if (!cast || !hostRef.current) return;
    const term = new Terminal({
      cursorBlink: false,
      disableStdin: true,
      convertEol: true,
      fontFamily: "JetBrains Mono, ui-monospace, monospace",
      fontSize: 13,
      theme: { background: "#070d0f", foreground: "#e8f0f2", cursor: "#e8a54b" },
      cols: cast.header.width || 120,
      rows: cast.header.height || 40,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(hostRef.current);
    try {
      fit.fit();
    } catch {
      /* ignore */
    }
    // Keep host height fixed so controls stay visible below the viewport.
    if (hostRef.current) {
      hostRef.current.style.overflow = "hidden";
    }
    termRef.current = term;
    return () => {
      if (timerRef.current) window.clearTimeout(timerRef.current);
      term.dispose();
      termRef.current = null;
    };
  }, [cast]);

  function applyEvent(ev: CastEvent) {
    const term = termRef.current;
    if (!term) return;
    if (ev.type === "o") term.write(ev.data);
    else if (ev.type === "r") {
      const m = /^(\d+)x(\d+)$/.exec(ev.data);
      if (m) {
        try {
          term.resize(Number(m[1]), Number(m[2]));
        } catch {
          /* ignore */
        }
      }
    }
  }

  function renderTo(t: number) {
    const term = termRef.current;
    if (!term || !cast) return;
    term.reset();
    idxRef.current = 0;
    while (idxRef.current < cast.events.length && cast.events[idxRef.current].t <= t + 1e-9) {
      applyEvent(cast.events[idxRef.current]);
      idxRef.current++;
    }
    setClock(t);
  }

  function stopTimer() {
    if (timerRef.current) window.clearTimeout(timerRef.current);
    timerRef.current = null;
    setPlaying(false);
  }

  function schedule(fromClock: number, fromIdx: number, spd: number) {
    if (!cast) return;
    if (fromIdx >= cast.events.length) {
      stopTimer();
      setClock(duration);
      return;
    }
    const next = cast.events[fromIdx];
    const delayMs = Math.max(0, ((next.t - fromClock) * 1000) / spd);
    timerRef.current = window.setTimeout(() => {
      applyEvent(next);
      const ni = fromIdx + 1;
      idxRef.current = ni;
      setClock(next.t);
      schedule(next.t, ni, spd);
    }, delayMs);
  }

  function play() {
    if (playing) {
      stopTimer();
      return;
    }
    let start = clock;
    if (start >= duration - 1e-6) {
      renderTo(0);
      start = 0;
    }
    setPlaying(true);
    schedule(start, idxRef.current, speed);
  }

  if (!cast) {
    return (
      <>
        <div className="row" style={{ justifyContent: "space-between" }}>
          <h3 style={{ margin: 0 }}>Recording {shortId(sessionId)}</h3>
          <a
            className="btn secondary sm"
            href={`data:text/plain;charset=utf-8,${encodeURIComponent(text)}`}
            download={`session-${sessionId.slice(0, 8)}.txt`}
          >
            Download
          </a>
        </div>
        <pre className="session">{text}</pre>
      </>
    );
  }

  return (
    <div className="cast-player">
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h3 style={{ margin: 0 }}>Recording {shortId(sessionId)}</h3>
        <a
          className="btn secondary sm"
          href={`data:application/x-asciicast;charset=utf-8,${encodeURIComponent(text)}`}
          download={`session-${sessionId.slice(0, 8)}.cast`}
        >
          Download
        </a>
      </div>
      <div className="cast-host" ref={hostRef} />
      <div className="cast-controls">
        <button type="button" className="btn sm" onClick={play}>
          {playing ? "Pause" : "Play"}
        </button>
        <button
          type="button"
          className="btn secondary sm"
          onClick={() => {
            stopTimer();
            renderTo(0);
            setPlaying(true);
            schedule(0, 0, speed);
          }}
        >
          Restart
        </button>
        <input
          type="range"
          min={0}
          max={Math.max(duration, 0.1)}
          step={0.05}
          value={clock}
          onChange={(e) => {
            const was = playing;
            stopTimer();
            renderTo(Number(e.target.value) || 0);
            if (was) {
              setPlaying(true);
              schedule(Number(e.target.value) || 0, idxRef.current, speed);
            }
          }}
        />
        <span className="mono muted">
          {fmtCastTime(clock)} / {fmtCastTime(duration)}
        </span>
        <select
          aria-label="Playback speed"
          value={speed}
          onChange={(e) => {
            const spd = Number(e.target.value) || 1;
            setSpeed(spd);
            if (playing) {
              if (timerRef.current) window.clearTimeout(timerRef.current);
              schedule(clock, idxRef.current, spd);
            }
          }}
        >
          <option value={0.5}>0.5×</option>
          <option value={1}>1×</option>
          <option value={2}>2×</option>
          <option value={4}>4×</option>
        </select>
      </div>
    </div>
  );
}
