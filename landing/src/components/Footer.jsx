import Logo from './Logo.jsx'

export default function Footer() {
  return (
    <footer style={{
      borderTop: '1px solid var(--border)',
      padding: '40px var(--pad)',
      display: 'flex', justifyContent: 'space-between', alignItems: 'center',
      flexWrap: 'wrap', gap: 20
    }}>
      <Logo size={18} />
      <div className="mono" style={{ color: 'var(--text-faint)' }}>
        © {new Date().getFullYear()} SAMY ABDELMALEK
      </div>
      <nav style={{ display: 'flex', gap: 20 }}>
        <a className="mono" href="https://github.com/itsaam/hatch" target="_blank" rel="noreferrer" style={{ color: 'var(--text-mute)' }}>GITHUB ↗</a>
        <a className="mono" href="https://samyabdelmalek.fr" style={{ color: 'var(--text-mute)' }}>PORTFOLIO ↗</a>
      </nav>
    </footer>
  )
}
