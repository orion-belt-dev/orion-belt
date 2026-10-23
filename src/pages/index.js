import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';

import Heading from '@theme/Heading';
import styles from './index.module.css';

function HomepageHeader() {
  return (
    <header className={styles.hero}>
      <div className={`container ${styles.heroInner}`}>
        <div className={styles.heroText}>
          <Heading as="h1" className={styles.heroTitle}>
            Orion Belt
          </Heading>
          <p className={styles.heroSubtitle}>
            Self-hosted SSH access gateway with PAM workflows
          </p>
          <p className={styles.heroDescription}>
            Teleport-like SSH access control without opening inbound ports or
            adopting a large platform. Agents dial out. Sessions are recorded.
            JIT approvals and MFA included.
          </p>
          <div className={styles.buttons}>
            <Link
              className={`button button--lg ${styles.ctaPrimary}`}
              to="/docs/quickstart/get-started">
              Get started
            </Link>
            <Link
              className={`button button--lg ${styles.ctaSecondary}`}
              to="/docs/quickstart/try-in-10-minutes">
              Try it in 10 minutes
            </Link>
          </div>
        </div>
        <img
          className={styles.heroMark}
          src={useBaseUrl('/img/orion-belt-mark-color-on-dark.svg')}
          alt=""
          width="320"
          height="320"
        />
      </div>
    </header>
  );
}

export default function Home() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`${siteConfig.title} — self-hosted SSH PAM`}
      description="Orion Belt — self-hosted SSH access gateway with PAM workflows. Agents dial out, sessions recorded, JIT approvals, MFA.">
      <HomepageHeader />
      <main>
        <HomepageFeatures />
      </main>
    </Layout>
  );
}
