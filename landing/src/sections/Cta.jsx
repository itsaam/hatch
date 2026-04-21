import Section from '../components/Section.jsx'
import RevealText from '../components/RevealText.jsx'
import EmailCapture from '../components/EmailCapture.jsx'

export default function Cta() {
  return (
    <Section id="cta" label="[06] EARLY ACCESS" style={{ position: 'relative', overflow: 'hidden' }}>
      <div style={{
        position: 'absolute', bottom: '-30%', left: '50%', transform: 'translateX(-50%)',
        width: 700, height: 700,
        background: 'radial-gradient(circle, rgba(255, 122, 61, 0.18) 0%, transparent 60%)',
        filter: 'blur(80px)', pointerEvents: 'none'
      }} />
      <div style={{ position: 'relative', textAlign: 'center', maxWidth: 900, margin: '0 auto' }}>
        <RevealText as="h2" className="display" style={{
          fontSize: 'clamp(40px, 7vw, 96px)', fontWeight: 800, lineHeight: 0.95
        }}>
          Sois prévenu au <span className="italic-accent" style={{ fontSize: 'inherit' }}>lancement.</span>
        </RevealText>

        <p style={{
          marginTop: 28, color: 'var(--text-mute)', fontSize: 17, maxWidth: 540, marginInline: 'auto'
        }}>
          Pas de spam. Un email quand la beta privée ouvre, et c'est tout.
        </p>

        <div style={{ marginTop: 40, display: 'grid', placeItems: 'center' }}>
          <EmailCapture size="lg" />
        </div>

        <div style={{
          marginTop: 60, paddingTop: 40, borderTop: '1px solid var(--border)',
          display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 16
        }}>
          <p className="mono" style={{ color: 'var(--text-faint)', fontSize: 12, letterSpacing: '0.08em', textTransform: 'uppercase', margin: 0 }}>
            Open source · MIT
          </p>
          <a href="https://github.com/itsaam/hatch" target="_blank" rel="noopener"
            style={{
              display: 'inline-flex', alignItems: 'center', gap: 10,
              padding: '14px 24px', borderRadius: 999,
              background: 'var(--surface)', border: '1px solid var(--border-strong)',
              color: 'var(--text)', fontSize: 15, fontWeight: 500,
              transition: 'all .25s var(--ease)'
            }}
            onMouseEnter={(e) => { e.currentTarget.style.borderColor = 'var(--accent)'; e.currentTarget.style.transform = 'translateY(-2px)'; e.currentTarget.style.boxShadow = '0 8px 30px rgba(255,122,61,0.15)' }}
            onMouseLeave={(e) => { e.currentTarget.style.borderColor = 'var(--border-strong)'; e.currentTarget.style.transform = 'translateY(0)'; e.currentTarget.style.boxShadow = 'none' }}
          >
            <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>
            <span style={{ fontWeight: 600 }}>Star on GitHub</span>
            <span className="mono" style={{ color: 'var(--text-faint)', fontSize: 12 }}>itsaam/hatch</span>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" style={{ color: 'var(--accent)' }}><path d="M8 .25a.75.75 0 0 1 .673.418l1.882 3.815 4.21.612a.75.75 0 0 1 .416 1.279l-3.046 2.97.719 4.192a.75.75 0 0 1-1.088.791L8 12.347l-3.766 1.98a.75.75 0 0 1-1.088-.79l.72-4.194L.818 6.374a.75.75 0 0 1 .416-1.28l4.21-.611L7.327.668A.75.75 0 0 1 8 .25z"/></svg>
          </a>
        </div>
      </div>
    </Section>
  )
}
