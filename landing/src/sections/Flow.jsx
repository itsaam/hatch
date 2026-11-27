import { useEffect, useRef } from 'react'
import gsap from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import Section from '../components/Section.jsx'

gsap.registerPlugin(ScrollTrigger)

const lines = [
  { t: 'PR comment: @hatchpr preview', kind: 'cmd' },
  { t: '→ Hatch validates the GitHub webhook', kind: 'log' },
  { t: '→ Builds the application image', kind: 'log' },
  { t: '→ Starts an isolated stack', kind: 'log' },
  { t: '→ Configures the preview route', kind: 'log' },
  { t: '→ Preview link posted on the PR', kind: 'success' },
  { t: '(PR closed) → preview resources removed', kind: 'mute' }
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
    <Section id="flow" label="[04] THE FLOW">
      <h2 className="display" style={{
        fontSize: 'clamp(32px, 5vw, 56px)', maxWidth: 800, marginBottom: 60, fontWeight: 700
      }}>
        See what happens after you request a preview.
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
