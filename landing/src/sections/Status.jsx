import Section from '../components/Section.jsx'

const items = [
  { s: 'done', t: 'Concept validé' },
  { s: 'done', t: 'Architecture définie' },
  { s: 'done', t: 'Control plane' },
  { s: 'done', t: 'Webhook GitHub handler' },
  { s: 'done', t: 'Docker orchestration' },
  { s: 'done', t: 'Traefik routing dynamique' },
  { s: 'done', t: 'Bot GitHub App (comments PR)' },
  { s: 'done', t: 'Cleanup auto (TTL + reconcile)' },
  { s: 'wip', t: 'Dashboard' },
  { s: 'todo', t: 'Beta privée' }
]

const symbol = { done: '☑', wip: '◐', todo: '☐' }
const color = { done: 'var(--accent-2)', wip: 'var(--accent)', todo: 'var(--text-faint)' }
const tlabel = { done: 'FAIT', wip: 'EN COURS', todo: 'À VENIR' }

export default function Status() {
  return (
    <Section id="status" label="[06] EN COURS">
      <h2 className="display" style={{
        fontSize: 'clamp(32px, 5vw, 56px)', maxWidth: 800, marginBottom: 60, fontWeight: 700
      }}>
        Là où on en est.
      </h2>

      <ul style={{
        listStyle: 'none', display: 'flex', flexDirection: 'column',
        border: '1px solid var(--border)', borderRadius: 16, overflow: 'hidden',
        background: 'var(--surface)'
      }}>
        {items.map((it, i) => (
          <li key={it.t} style={{
            display: 'grid', gridTemplateColumns: '60px 1fr auto',
            alignItems: 'center', gap: 16,
            padding: '20px 24px',
            borderTop: i ? '1px solid var(--border)' : 'none'
          }}>
            <span style={{ fontSize: 20, color: color[it.s], fontFamily: 'var(--font-mono)' }}>{symbol[it.s]}</span>
            <span style={{
              fontFamily: 'var(--font-display)', fontSize: 18, fontWeight: 500,
              color: it.s === 'todo' ? 'var(--text-mute)' : 'var(--text)',
              textDecoration: it.s === 'done' ? 'line-through' : 'none',
              textDecorationColor: 'var(--text-faint)'
            }}>{it.t}</span>
            <span className="mono" style={{ color: color[it.s] }}>{tlabel[it.s]}</span>
          </li>
        ))}
      </ul>
    </Section>
  )
}
