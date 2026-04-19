import Section from '../components/Section.jsx'

const cards = [
  { vs: 'vs Vercel', t: 'Pour autre chose que Next.js', d: 'Django, .NET, Go, Rails, Docker custom — Hatch déploie tout ce qui tient dans un container.' },
  { vs: 'vs Coolify', t: 'Focalisé sur le PR preview', d: 'Pas un PaaS complet à configurer. Juste l\'éphémère par PR, point.' },
  { vs: 'vs un staging partagé', t: 'Une preview par PR, isolée', d: 'Plus de conflits entre features. Chaque branche a son URL, sa DB, ses env.' },
  { vs: 'vs rien', t: 'Le designer voit le rendu', d: 'Et le PM valide pour de vrai, pas à l\'aveugle sur un titre de PR.' }
]

export default function Why() {
  return (
    <Section id="why" label="[04] POURQUOI">
      <h2 className="display" style={{
        fontSize: 'clamp(32px, 5vw, 56px)', maxWidth: 900, marginBottom: 60, fontWeight: 700
      }}>
        Pas un autre PaaS. Une chose, bien faite.
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
