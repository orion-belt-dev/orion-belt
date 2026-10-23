import styles from './styles.module.css';

const FeatureList = [
  {
    title: 'Agents dial out',
    description:
      'Targets open a reverse SSH tunnel to the gateway. You don’t punch inbound holes on every host just to get a bastion.',
  },
  {
    title: 'Sessions are recorded',
    description:
      'SSH and web-terminal sessions go through the gateway, so you can replay them later or live-watch an active one.',
  },
  {
    title: 'Access can expire',
    description:
      'People request time-boxed access; an admin (or ChatOps) approves it. When the window ends, the grant is gone.',
  },
  {
    title: 'MFA when you want it',
    description:
      'TOTP and WebAuthn for the console, plus normal SSH keys — including FIDO security keys if that’s how you work.',
  },
  {
    title: 'Permissions per machine',
    description:
      'Built-in ReBAC decides who can reach which agent and as which remote user. Optional OpenFGA if you already run that.',
  },
  {
    title: 'You run it',
    description:
      'Docker compose for a lab, packages for a real install. No SaaS dependency. Apache 2.0 + Commons Clause — free to self-host.',
  },
];

function Feature({title, description}) {
  return (
    <li className={styles.item}>
      <h3 className={styles.title}>{title}</h3>
      <p className={styles.body}>{description}</p>
    </li>
  );
}

export default function HomepageFeatures() {
  return (
    <section className={styles.features}>
      <div className="container">
        <h2 className={styles.heading}>What you actually get</h2>
        <ul className={styles.list}>
          {FeatureList.map((props) => (
            <Feature key={props.title} {...props} />
          ))}
        </ul>
      </div>
    </section>
  );
}
