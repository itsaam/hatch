import Section from '../components/Section.jsx'

const cards = [
  { vs: 'Application stack', t: 'Run the whole stack', d: 'Preview your app alongside PostgreSQL, Redis or a background worker.' },
  { vs: 'Workflow', t: 'Start from the PR', d: 'Request a preview when reviewers need it and close the PR to remove it.' },
  { vs: 'Data', t: 'Separate preview environments', d: 'Each preview can run its own database, services and environment variables.' },
  { vs: 'Review', t: 'Share a link', d: 'Reviewers can see the running app without installing its stack.' }
]

export default function Why() {
  return (
    <Section id="why" label="[05] WHY HATCH">
      <h2 className="display" style={{
        fontSize: 'clamp(32px, 5vw, 56px)', maxWidth: 900, marginBottom: 60, fontWeight: 700
      }}>
        What you can review with Hatch.
      </h2>

      <div style={{
        display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 16
      }}>
        {cards.map((c) => (
          <article key={c.vs} style={{
            padding: 28, background: 'var(--surface)',
            border: '1px solid var(--border)', borderRadius: 16,
            transition: 'transform .35s var(--ease-bounce), border-color .25s var(--ease)'
          }}
            onMouseEnter={(e) => {
              e.currentTarget.style.transform = 'translateY(-4px)'
              e.currentTarget.style.borderColor = 'var(--border-accent)'
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.transform = 'translateY(0)'
              e.currentTarget.style.borderColor = 'var(--border)'
            }}
          >
            <div className="mono" style={{ color: 'var(--accent)', marginBottom: 20 }}>{c.vs}</div>
            <h3 className="display" style={{ fontSize: 22, fontWeight: 600, marginBottom: 12, lineHeight: 1.15 }}>{c.t}</h3>
            <p style={{ color: 'var(--text-mute)', fontSize: 14.5 }}>{c.d}</p>
          </article>
        ))}
      </div>
    </Section>
  )
}
