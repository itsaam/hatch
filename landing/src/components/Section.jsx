export default function Section({ id, label, children, style }) {
  return (
    <section id={id} style={{
      padding: 'clamp(80px, 12vw, 160px) 0',
      borderTop: '1px solid var(--border)',
      ...style
    }}>
      <div className="container">
        {label && (
          <div className="mono" style={{ marginBottom: 40, color: 'var(--accent)' }}>
            {label}
          </div>
        )}
        {children}
      </div>
    </section>
  )
}
