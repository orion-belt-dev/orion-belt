import {useState} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import Belt from '@site/src/components/Belt';

import styles from './index.module.css';

const QUICKSTART = `# server (Linux)
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- server
# each machine you want to reach
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- agent
# your computer: osh, ocp, oadmin
curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh`;

// What people put up with today, and what replaces it. Each row is a real
// default of the product, not an aspiration.
const CHANGES = [
  {
    before: 'A bastion with port 22 open to the internet, and every target reachable from it.',
    after: 'Agents dial out to the gateway. Targets accept no inbound connections at all.',
  },
  {
    before: 'SSH keys copied to servers years ago, still valid for people who left.',
    after: 'Access is a grant with an expiry. Short-lived SSH certificates if you turn on the CA.',
  },
  {
    before: 'A shared kubeconfig with cluster-admin in a team vault.',
    after: 'A RoleBinding for one namespace, issued on approval and deleted when the grant ends.',
  },
  {
    before: 'RDP on 3389 behind a VPN, with no record of who connected.',
    after: 'RDP and VNC through the same agents, each connection audited with who, when and how much.',
  },
];

const FLOW = [
  {
    title: 'Ask',
    body: 'Request time-boxed access from the terminal or the console, with a reason.',
    code: 'osh root@web-01 -r --reason "INC-42" -d 1800',
  },
  {
    title: 'Approve',
    body: 'An admin approves in the console or Slack, or a risk policy approves low-risk requests on its own.',
  },
  {
    title: 'Connect',
    body: 'Your usual client, through the gateway. OpenSSH, RDP and VNC clients, and kubectl all work.',
    code: 'osh root@web-01',
  },
  {
    title: 'Expire',
    body: 'When the window closes the grant is gone, and any Kubernetes binding with it. Nobody has to remember.',
  },
];

const PROTOCOLS = [
  {name: 'SSH and SCP', how: 'osh, ocp or plain OpenSSH', trail: 'Recorded, replayable, live-watchable', recorded: true},
  {name: 'Web terminal', how: 'From the console, in the browser', trail: 'Recorded, replayable, live-watchable', recorded: true},
  {name: 'RDP', how: 'osh rdp, or ssh -L to port 3389', trail: 'Audited: who, when, bytes, duration'},
  {name: 'VNC', how: 'osh vnc, or ssh -L to port 5900', trail: 'Audited: who, when, bytes, duration'},
  {name: 'Kubernetes', how: 'osh kube, then kubectl as usual', trail: 'Audited, plus every binding issued and removed'},
];

const FAQ = [
  {
    q: 'Do targets need any inbound port open?',
    a: 'No. Each agent opens an outbound connection to the gateway and every session rides it. Only the gateway listens.',
  },
  {
    q: 'Can people keep their own SSH client?',
    a: 'Yes. ssh alice+web-01@gateway works with stock OpenSSH, as do Ansible and AWX. osh adds access requests, RDP, VNC and Kubernetes on top.',
  },
  {
    q: 'How does Kubernetes access end?',
    a: 'The gateway deletes the RoleBinding when the grant expires or is revoked, and checks every 30 seconds. The in-cluster agent also deletes expired bindings on its own, so access ends even if the gateway is down.',
  },
  {
    q: 'Are RDP and VNC sessions recorded?',
    a: 'Not yet. Each connection is audited, but the stream is encrypted end to end, so recording needs the gateway to terminate the protocol. That is on the roadmap.',
  },
  {
    q: 'What does it cost?',
    a: 'Nothing to self-host and use internally. It is Apache 2.0 with the Commons Clause: you may not sell Orion Belt itself as a product or hosted service.',
  },
  {
    q: 'What do I need to run it?',
    a: 'One Linux server with 2 cores and 4 GB of RAM, and PostgreSQL. Docker Compose for a lab, deb, rpm and apk packages for a real install.',
  },
];

function CopyCommand({code, label}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div className={styles.command}>
      {/* Focusable so a keyboard can scroll a line wider than the screen. */}
      <pre className={styles.commandCode} tabIndex={0} aria-label={label}>
        <code>{code}</code>
      </pre>
      <button type="button" className={styles.copy} onClick={copy} aria-label={copied ? 'Copied' : `Copy ${label}`}>
        {copied ? 'Copied' : 'Copy'}
      </button>
      <span className="sr-only" aria-live="polite">
        {copied ? 'Copied to clipboard' : ''}
      </span>
    </div>
  );
}

function Hero() {
  return (
    <header className={styles.hero}>
      <div className={`container ${styles.heroInner}`}>
        <div className={styles.heroText}>
          <Heading as="h1" className={styles.heroTitle}>
            Reach every server, desktop and cluster without opening a port.
          </Heading>
          <p className={styles.heroLead}>
            Orion Belt is an access gateway you run yourself. Agents dial out to it, people ask for
            time-boxed access, and SSH sessions are recorded. SSH, RDP, VNC and Kubernetes, one
            audit trail.
          </p>
          <div className={styles.buttons}>
            <Link className={`button button--lg ${styles.ctaPrimary}`} to="/docs/quickstart/get-started">
              Get started in 10 minutes
            </Link>
            <Link className={`button button--lg ${styles.ctaSecondary}`} to="/docs/quickstart/try-in-10-minutes">
              Try it with Docker
            </Link>
          </div>
          <p className={styles.heroMeta}>
            Free to self-host. Version 1.4.0.{' '}
            <Link className={styles.heroLink} to="https://github.com/orion-belt-dev/orion-belt">
              Source on GitHub
            </Link>
          </p>
        </div>
        <Belt />
      </div>
    </header>
  );
}

function Changes() {
  return (
    <section className={styles.section} aria-labelledby="changes">
      <div className="container">
        <Heading as="h2" id="changes" className={styles.h2}>
          What changes on day one
        </Heading>
        <p className={styles.intro}>
          Access tends to sprawl quietly: one more open port, one more key, one more shared
          credential. These are the defaults Orion Belt replaces.
        </p>
        <table className={styles.changes}>
          <thead>
            <tr>
              <th scope="col">Without it</th>
              <th scope="col">With Orion Belt</th>
            </tr>
          </thead>
          <tbody>
            {CHANGES.map((row) => (
              <tr key={row.after}>
                <td>{row.before}</td>
                <td>{row.after}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Flow() {
  return (
    <section className={`${styles.section} ${styles.sectionAlt}`} aria-labelledby="flow">
      <div className="container">
        <Heading as="h2" id="flow" className={styles.h2}>
          From request to expiry
        </Heading>
        <ol className={styles.flow}>
          {FLOW.map((step) => (
            <li key={step.title} className={styles.step}>
              <Heading as="h3" className={styles.stepTitle}>
                {step.title}
              </Heading>
              <p>{step.body}</p>
              {step.code && <code className={styles.stepCode}>{step.code}</code>}
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

function Protocols() {
  return (
    <section className={styles.section} aria-labelledby="protocols">
      <div className={`container ${styles.split}`}>
        <div>
          <Heading as="h2" id="protocols" className={styles.h2}>
            One gateway for every way in
          </Heading>
          <p className={styles.intro}>
            Every protocol uses the same agents, the same grants and the same approval. A grant
            for one protocol never opens another.
          </p>
          <p className={styles.links}>
            <Link to="/docs/guides/rdp-vnc">RDP and VNC guide</Link>
            <Link to="/docs/guides/kubernetes">Kubernetes guide</Link>
          </p>
        </div>
        <div className={styles.tableScroll}>
          <table className={styles.protocols}>
            <thead>
              <tr>
                <th scope="col">Protocol</th>
                <th scope="col">How you connect</th>
                <th scope="col">What the gateway keeps</th>
              </tr>
            </thead>
            <tbody>
              {PROTOCOLS.map((p) => (
                <tr key={p.name}>
                  <th scope="row">{p.name}</th>
                  <td>{p.how}</td>
                  <td>
                    {p.recorded && <span className={styles.recDot} aria-hidden="true" />}
                    {p.trail}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function Assurance() {
  return (
    <section className={`${styles.section} ${styles.sectionAlt}`} aria-labelledby="assurance">
      <div className="container">
        <Heading as="h2" id="assurance" className={styles.h2}>
          Answers for the people who sign off
        </Heading>
        <dl className={styles.assurance}>
          <div>
            <dt>Who accessed what, and who approved it</dt>
            <dd>
              SOC 2, ISO 27001 and PCI reports as CSV, PDF or JSON. Every session is tied to the
              request or grant that allowed it.{' '}
              <Link to="/docs/guides/compliance-reports">Compliance reports</Link>
            </dd>
          </div>
          <div>
            <dt>Why someone can, or can't, get in</dt>
            <dd>
              <code>osh explain root@web-01</code> walks the exact grants and roles the gateway
              checked. Denied sessions print the same answer.
            </dd>
          </div>
          <div>
            <dt>Requests that look wrong</dt>
            <dd>
              Each request records source, device, MFA and target sensitivity. A risk policy can
              send risky ones to an admin, and unusual sessions are flagged for review.{' '}
              <Link to="/docs/guides/risk-and-anomalies">Risk and anomalies</Link>
            </dd>
          </div>
          <div>
            <dt>Strong sign-in</dt>
            <dd>
              TOTP and WebAuthn for the console, FIDO security keys over SSH, and an optional SSH
              certificate authority for short-lived certificates.
            </dd>
          </div>
        </dl>
      </div>
    </section>
  );
}

function RunIt() {
  return (
    <section className={styles.section} aria-labelledby="run">
      <div className={`container ${styles.split}`}>
        <div>
          <Heading as="h2" id="run" className={styles.h2}>
            Runs on your hardware
          </Heading>
          <p className={styles.intro}>
            No SaaS, no phone-home. Signed packages for Debian, Ubuntu, Fedora, RHEL, openSUSE,
            Alpine and Arch, and clients for macOS, Windows and the BSDs, all upgraded by your
            package manager. The guide takes you from install to a recorded session per system.
          </p>
          <p className={styles.links}>
            <Link to="/docs/quickstart/get-started">Step-by-step for your OS</Link>
            <Link to="/docs/quickstart/installation">All install options</Link>
            <Link to="https://pkgs.orion-belt.dev">Package repositories</Link>
          </p>
        </div>
        <CopyCommand code={QUICKSTART} label="quickstart commands" />
      </div>
    </section>
  );
}

function Faq() {
  return (
    <section className={`${styles.section} ${styles.sectionAlt}`} aria-labelledby="faq">
      <div className={`container ${styles.faqWrap}`}>
        <Heading as="h2" id="faq" className={styles.h2}>
          Questions people ask first
        </Heading>
        <div className={styles.faq}>
          {FAQ.map((item) => (
            <details key={item.q} className={styles.faqItem}>
              <summary>{item.q}</summary>
              <p>{item.a}</p>
            </details>
          ))}
        </div>
      </div>
    </section>
  );
}

function Closing() {
  return (
    <section className={styles.closing} aria-labelledby="closing">
      <div className={`container ${styles.closingInner}`}>
        <Heading as="h2" id="closing" className={styles.closingTitle}>
          Agents dial out. Sessions are recorded. Access expires on its own.
        </Heading>
        <div className={styles.buttons}>
          <Link className={`button button--lg ${styles.ctaPrimary}`} to="/docs/quickstart/get-started">
            Get started in 10 minutes
          </Link>
          <Link className={`button button--lg ${styles.ctaSecondary}`} to="/docs/introduction">
            Read the docs
          </Link>
        </div>
      </div>
    </section>
  );
}

export default function Home() {
  return (
    <Layout
      title="Self-hosted access gateway for SSH, RDP, VNC and Kubernetes"
      description="Orion Belt is a self-hosted access gateway. Agents dial out, so targets open no ports. Time-boxed access, recorded SSH sessions, RDP, VNC and just-in-time Kubernetes RoleBindings.">
      <Hero />
      <main>
        <Changes />
        <Flow />
        <Protocols />
        <Assurance />
        <RunIt />
        <Faq />
        <Closing />
      </main>
    </Layout>
  );
}
