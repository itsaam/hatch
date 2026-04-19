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
      <StatusPill />
    </header>
  )
}
