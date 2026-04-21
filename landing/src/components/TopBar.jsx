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
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
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
        <a href="/app/" className="mono" style={{
          fontSize: 12, letterSpacing: '0.08em',
          color: 'var(--text-mute)', textTransform: 'uppercase',
          padding: '6px 12px', borderRadius: 999,
          border: '1px solid var(--border)',
          transition: 'all .25s var(--ease)'
        }}
        onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--text)'; e.currentTarget.style.borderColor = 'var(--border-strong)' }}
        onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--text-mute)'; e.currentTarget.style.borderColor = 'var(--border)' }}
        >APP</a>
        <a href="https://github.com/itsaam/hatch" target="_blank" rel="noopener" className="mono" style={{
          fontSize: 12, letterSpacing: '0.08em',
          color: 'var(--text)', textTransform: 'uppercase',
          padding: '6px 12px', borderRadius: 999,
          border: '1px solid var(--border-strong)',
          display: 'inline-flex', alignItems: 'center', gap: 6,
          transition: 'all .25s var(--ease)'
        }}
        onMouseEnter={(e) => { e.currentTarget.style.borderColor = 'var(--accent)'; e.currentTarget.style.color = 'var(--accent)' }}
        onMouseLeave={(e) => { e.currentTarget.style.borderColor = 'var(--border-strong)'; e.currentTarget.style.color = 'var(--text)' }}
        >
          <svg width="12" height="12" viewBox="0 0 16 16" fill="currentColor"><path d="M8 .25a.75.75 0 0 1 .673.418l1.882 3.815 4.21.612a.75.75 0 0 1 .416 1.279l-3.046 2.97.719 4.192a.75.75 0 0 1-1.088.791L8 12.347l-3.766 1.98a.75.75 0 0 1-1.088-.79l.72-4.194L.818 6.374a.75.75 0 0 1 .416-1.28l4.21-.611L7.327.668A.75.75 0 0 1 8 .25z"/></svg>
          STAR
        </a>
        <StatusPill />
      </div>
    </header>
  )
}
