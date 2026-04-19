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
      </div>
    </Section>
  )
}
