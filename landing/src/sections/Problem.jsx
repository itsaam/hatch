import Section from '../components/Section.jsx'
import RevealText from '../components/RevealText.jsx'

const items = [
  { n: '01', t: 'Cloner le repo', d: 'Pull, switch de branche, npm install, attendre.' },
  { n: '02', t: 'Démarrer la stack', d: 'Postgres, Redis, le worker, les variables d\'env.' },
  { n: '03', t: 'Espérer que ça marche', d: 'Une dépendance bizarre, un port pris, un .env manquant.' }
]

export default function Problem() {
  return (
    <Section id="problem" label="[01] LE PROBLÈME">
      <RevealText as="h2" className="display" style={{
        fontSize: 'clamp(32px, 5vw, 64px)', maxWidth: 900, marginBottom: 80
      }}>
        Pour tester une PR, il faut encore galérer.
      </RevealText>

      <div style={{
        display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: 24
      }}>
        {items.map((it) => (
          <div key={it.n} style={{
            padding: '32px 28px', border: '1px solid var(--border)',
            background: 'var(--surface)', borderRadius: 16, position: 'relative'
          }}>
            <div className="mono" style={{ color: 'var(--accent)', marginBottom: 24 }}>{it.n}</div>
            <h3 className="display" style={{ fontSize: 28, fontWeight: 600, marginBottom: 12 }}>{it.t}</h3>
            <p style={{ color: 'var(--text-mute)', fontSize: 15 }}>{it.d}</p>
          </div>
        ))}
      </div>

      <RevealText as="p" className="display" style={{
        marginTop: 100, fontSize: 'clamp(28px, 4vw, 52px)',
        fontWeight: 600, maxWidth: 1000, lineHeight: 1.1
      }}>
        Le designer abandonne. Le PM valide à l'aveugle. Les bugs passent.
      </RevealText>
    </Section>
  )
}
