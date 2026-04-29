import { useEffect, useRef } from 'react'
import gsap from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import Section from '../components/Section.jsx'

gsap.registerPlugin(ScrollTrigger)

const lines = [
  { t: '$ git push origin feature/wishlist', kind: 'cmd' },
  { t: '→ Hatch reçoit le webhook GitHub', kind: 'log' },
  { t: '→ Build l\'image Docker', kind: 'log' },
  { t: '→ Spawn un container isolé', kind: 'log' },
  { t: '→ Configure le routing wildcard', kind: 'log' },
  { t: '🚀 Preview live : pr-42-althea.preview.tonsite.fr', kind: 'success' },
  { t: '(merged) → cleanup automatique', kind: 'mute' }
]

export default function Flow() {
  const ref = useRef(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const items = el.querySelectorAll('.line')
    gsap.set(items, { x: -16, opacity: 0 })

    ScrollTrigger.create({
      trigger: el, start: 'top 70%', once: true,
      onEnter: () => {
        gsap.to(items, { x: 0, opacity: 1, duration: 0.5, ease: 'power3.out', stagger: 0.12 })
      }
    })
  }, [])

  return (
    <Section id="flow" label="[04] LE FLUX">
      <h2 className="display" style={{
        fontSize: 'clamp(32px, 5vw, 56px)', maxWidth: 800, marginBottom: 60, fontWeight: 700
      }}>
        Du push au lien live, en moins d'une minute.
      </h2>

      <div ref={ref} style={{
        background: '#0A0907', borderRadius: 16,
        border: '1px solid var(--border-strong)',
        padding: 'clamp(24px, 4vw, 48px)',
        fontFamily: 'var(--font-mono)', fontSize: 14,
        lineHeight: 2, position: 'relative', overflow: 'hidden'
      }}>
        <div style={{
          display: 'flex', gap: 6, marginBottom: 24,
          paddingBottom: 20, borderBottom: '1px solid var(--border)'
        }}>
          <Dot c="#FF5F57"/><Dot c="#FFBD2E"/><Dot c="#28C840"/>
          <span style={{ marginLeft: 12, color: 'var(--text-faint)', fontSize: 12 }}>~ hatch.log</span>
        </div>

        {lines.map((l, i) => (
          <div key={i} className="line" style={{
            color: colorFor(l.kind),
            fontWeight: l.kind === 'success' ? 500 : 400
          }}>
            {l.kind === 'cmd' && <span style={{ color: 'var(--text-faint)', marginRight: 8 }}>{String(i+1).padStart(2, '0')}</span>}
            {l.kind !== 'cmd' && <span style={{ color: 'var(--text-faint)', marginRight: 8 }}>{String(i+1).padStart(2, '0')}</span>}
            {l.t}
          </div>
        ))}
      </div>
    </Section>
  )
}

const Dot = ({ c }) => <span style={{ width: 11, height: 11, borderRadius: '50%', background: c }}/>

function colorFor(k) {
  if (k === 'cmd') return 'var(--text)'
  if (k === 'success') return 'var(--accent)'
  if (k === 'mute') return 'var(--text-faint)'
  return 'var(--text-mute)'
}
