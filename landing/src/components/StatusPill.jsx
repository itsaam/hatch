export default function StatusPill({ children = 'EN DÉVELOPPEMENT' }) {
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 8,
      padding: '6px 12px', borderRadius: 999,
      border: '1px solid var(--border-accent)',
      background: 'rgba(255, 122, 61, 0.06)',
      fontFamily: 'var(--font-mono)', fontSize: 11,
      letterSpacing: '0.12em', textTransform: 'uppercase',
      color: 'var(--text)'
    }}>
      <span style={{
        width: 7, height: 7, borderRadius: '50%',
        background: 'var(--accent)',
        boxShadow: '0 0 12px var(--accent)',
        animation: 'pulse 2s var(--ease) infinite'
      }} />
      {children}
      <style>{`
        @keyframes pulse {
          0%, 100% { opacity: 1; transform: scale(1); }
          50% { opacity: 0.4; transform: scale(0.85); }
        }
      `}</style>
    </span>
  )
}
