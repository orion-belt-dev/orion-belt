import {useId, useRef, useState} from 'react';
import styles from './styles.module.css';

// The mark, drawn as the thing it stands for: your machine, the gateway in
// its ring, and the agent on the target. Geometry is the brand mark's own
// (96 × 96 viewBox), so the hero and the logo are the same picture.
const LAPTOP = {x: 20, y: 62};
const GATEWAY = {x: 48, y: 48};
const AGENT = {x: 76, y: 34};
const PATH = `M${LAPTOP.x} ${LAPTOP.y} L${AGENT.x} ${AGENT.y}`;

// Each protocol shows what osh really prints (cmd/osh). `trail` is what the
// gateway keeps: SSH is recorded; RDP, VNC and kubectl are audited only.
const PROTOCOLS = [
  {
    id: 'ssh',
    label: 'SSH',
    target: 'web-01',
    kind: 'Linux server',
    trail: 'recording',
    note: 'Recorded and live-watchable. Plain OpenSSH works too.',
    lines: [
      {cmd: 'osh root@web-01'},
      {out: 'root@web-01:~# systemctl restart nginx'},
      {out: 'root@web-01:~# exit'},
    ],
  },
  {
    id: 'rdp',
    label: 'RDP',
    target: 'win-01',
    kind: 'Windows desktop',
    trail: 'audited',
    note: 'Any RDP client, pointed at a port on your own machine.',
    lines: [
      {cmd: 'osh rdp win-01'},
      {out: 'RDP to win-01 on 127.0.0.1:53211'},
      {out: 'Connect your RDP client (mstsc, Remmina, FreeRDP) to 127.0.0.1:53211. Ctrl-C to close.'},
    ],
  },
  {
    id: 'vnc',
    label: 'VNC',
    target: 'lab-02',
    kind: 'Lab workstation',
    trail: 'audited',
    note: 'The agent decides where VNC goes, never the gateway.',
    lines: [
      {cmd: 'osh vnc lab-02'},
      {out: 'VNC to lab-02 on 127.0.0.1:59012'},
      {out: 'Connect your VNC viewer to 127.0.0.1:59012. Ctrl-C to close.'},
    ],
  },
  {
    id: 'k8s',
    label: 'Kubernetes',
    target: 'prod-eu',
    kind: 'Kubernetes cluster',
    trail: 'audited',
    note: 'kubectl acts as you, with only the RoleBinding you were granted.',
    lines: [
      {cmd: 'osh kube prod-eu'},
      {out: 'Kubernetes API of prod-eu relayed on 127.0.0.1:53817'},
      {out: 'export KUBECONFIG=~/.orion-belt/kube/prod-eu.yaml'},
    ],
  },
];

export default function Belt() {
  const [active, setActive] = useState(0);
  const tabs = useRef([]);
  const id = useId();
  const p = PROTOCOLS[active];

  const select = (i) => {
    setActive(i);
    tabs.current[i]?.focus();
  };

  const onKeyDown = (e) => {
    const last = PROTOCOLS.length - 1;
    const moves = {
      ArrowRight: active === last ? 0 : active + 1,
      ArrowLeft: active === 0 ? last : active - 1,
      Home: 0,
      End: last,
    };
    if (e.key in moves) {
      e.preventDefault();
      select(moves[e.key]);
    }
  };

  return (
    <figure className={styles.belt}>
      <div className={styles.tabs} role="tablist" aria-label="Protocol" onKeyDown={onKeyDown}>
        {PROTOCOLS.map((proto, i) => (
          <button
            key={proto.id}
            ref={(el) => (tabs.current[i] = el)}
            type="button"
            role="tab"
            id={`${id}-tab-${proto.id}`}
            aria-selected={i === active}
            aria-controls={`${id}-panel`}
            tabIndex={i === active ? 0 : -1}
            className={styles.tab}
            onClick={() => setActive(i)}>
            {proto.label}
          </button>
        ))}
      </div>

      <div
        className={styles.panel}
        role="tabpanel"
        id={`${id}-panel`}
        aria-labelledby={`${id}-tab-${p.id}`}>
        <div className={styles.stage}>
          <svg className={styles.svg} viewBox="0 0 96 96" aria-hidden="true">
            <path className={styles.track} d={PATH} />
            {/* Remounted per protocol: the connection is drawn again from
                you, through the gateway, to this target. */}
            <g key={p.id}>
              <path className={styles.lit} d={PATH} pathLength="1" />
              <circle className={styles.packet} r="2.2" cx={LAPTOP.x} cy={LAPTOP.y} />
            </g>
            <path
              className={styles.ring}
              d="M77.99 47.16A30 30 0 0 1 29.37 71.51M18.01 48.84A30 30 0 0 1 66.63 24.49"
            />
            <circle className={styles.star} cx={LAPTOP.x} cy={LAPTOP.y} r="5.5" />
            <circle key={`agent-${p.id}`} className={styles.agent} cx={AGENT.x} cy={AGENT.y} r="5.5" />
            <circle className={styles.gateway} cx={GATEWAY.x} cy={GATEWAY.y} r="9" />
          </svg>

          <span className={`${styles.label} ${styles.labelLaptop}`}>
            <strong>You</strong>
            <span>your usual client</span>
          </span>
          <span className={`${styles.label} ${styles.labelGateway}`}>
            <strong>Gateway</strong>
            <span>checks the grant</span>
          </span>
          <span key={p.id} className={`${styles.label} ${styles.labelAgent}`}>
            <strong>{p.target}</strong>
            <span>{p.kind}</span>
            <span>no inbound port</span>
          </span>
        </div>

        <div key={p.id} className={styles.terminal}>
          {p.lines.map((line, i) =>
            line.cmd ? (
              <div key={i} className={styles.cmd}>
                <span className={styles.prompt} aria-hidden="true">$ </span>
                <code>{line.cmd}</code>
              </div>
            ) : (
              <div key={i} className={styles.out}>
                {line.out}
              </div>
            ),
          )}
        </div>

        <figcaption className={styles.caption} aria-live="polite">
          {p.trail === 'recording' ? (
            <span className={styles.recording}>
              <span className={styles.recDot} aria-hidden="true" />
              Recording
            </span>
          ) : (
            <span className={styles.audited}>Audited, not recorded</span>
          )}
          <span>{p.note}</span>
        </figcaption>
      </div>
    </figure>
  );
}
