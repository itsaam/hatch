export default function Logo({ size = 22 }) {
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 8,
      fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: size, letterSpacing: '-0.02em'
    }}>
      <svg width={size + 2} height={size + 2} viewBox="0 0 32 32" aria-hidden>
        <path d="M7 5 L7 14 L13 12 L19 14 L25 12 L25 5" stroke="currentColor" strokeWidth="3" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
        <path d="M7 14 L13 12 L19 14 L25 12" stroke="var(--accent)" strokeWidth="2" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
        <path d="M7 14 L7 27 L25 27 L25 14" stroke="currentColor" strokeWidth="3" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
      Hatch
    </span>
  )
}
