export default function Marquee({ items = [], speed = 40 }) {
  const content = [...items, ...items, ...items]
  return (
    <div style={{
      overflow: 'hidden', borderTop: '1px solid var(--border)',
      borderBottom: '1px solid var(--border)', padding: '18px 0',
      background: 'var(--surface)', maskImage: 'linear-gradient(90deg, transparent, #000 8%, #000 92%, transparent)'
    }}>
      <div style={{
        display: 'inline-flex', gap: 48, whiteSpace: 'nowrap',
        animation: `scroll ${speed}s linear infinite`
      }}>
        {content.map((it, i) => (
          <span key={i} className="mono" style={{ color: 'var(--text-mute)' }}>
            {it} <span style={{ color: 'var(--accent)' }}>◇</span>
          </span>
        ))}
      </div>
      <style>{`@keyframes scroll { from { transform: translateX(0) } to { transform: translateX(-33.333%) } }`}</style>
    </div>
  )
}
