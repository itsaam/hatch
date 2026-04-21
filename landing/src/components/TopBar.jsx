import Logo from './Logo.jsx'
import StatusPill from './StatusPill.jsx'

export default function TopBar() {
  return (
    <header style={{
      position: 'fixed', top: 0, left: 0, right: 0, zIndex: 100,
      padding: '20px var(--pad)',
      display: 'flex', justifyContent: 'space-between', alignItems: 'center',
      backdropFilter: 'blur(12px)',
      background: 'linear-gradient(180deg, rgba(15,13,10,0.7) 0%, rgba(15,13,10,0) 100%)'
    }}>
      <Logo />
      <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
        <a href="/docs/" className="mono" style={{
          fontSize: 12, letterSpacing: '0.08em',
          color: 'var(--text-mute)', textTransform: 'uppercase',
          padding: '6px 12px', borderRadius: 999,
          border: '1px solid var(--border)',
          transition: 'all .25s var(--ease)'
        }}
        onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--text)'; e.currentTarget.style.borderColor = 'var(--border-strong)' }}
        onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-mute)'; e.currentTarget.style.borderColor = 'var(--border)' }}
        >DOCS</a>
        <StatusPill />
      </div>
    </header>
  )
}
