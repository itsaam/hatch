import Section from '../components/Section.jsx'

const items = [
  { s: 'done', t: 'On-demand previews' },
  { s: 'done', t: 'Repository allowlist' },
  { s: 'done', t: 'Control plane' },
  { s: 'done', t: 'GitHub webhooks' },
  { s: 'done', t: 'Docker Compose deployment' },
  { s: 'done', t: 'Traefik routing' },
  { s: 'done', t: 'GitHub PR comments' },
  { s: 'done', t: 'Cleanup on close or expiry' },
  { s: 'done', t: 'Dashboard and logs' },
  { s: 'wip', t: 'Kubernetes rollout validation' }
]

const symbol = { done: '☑', wip: '◐', todo: '☐' }
const color = { done: 'var(--accent-2)', wip: 'var(--accent)', todo: 'var(--text-faint)' }
const tlabel = { done: 'DONE', wip: 'IN PROGRESS', todo: 'PLANNED' }

export default function Status() {
  return (
    <Section id="status" label="[06] PROJECT STATUS">
      <h2 className="display" style={{
        fontSize: 'clamp(32px, 5vw, 56px)', maxWidth: 800, marginBottom: 60, fontWeight: 700
      }}>
        Project status.
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
