import { useEffect, useRef } from 'react'
import gsap from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import Section from '../components/Section.jsx'
import RevealText from '../components/RevealText.jsx'

gsap.registerPlugin(ScrollTrigger)

const steps = [
  { icon: 'push', label: 'git push', sub: 'Tu push une branche, c\'est tout.' },
  { icon: 'hatch', label: 'Hatch éclot', sub: 'Webhook, build, container, routing.' },
  { icon: 'click', label: 'Tout le monde teste', sub: 'Lien live commenté sur la PR.' }
]

const bullets = [
  'Aucune installation côté reviewer',
  'Isolation totale par PR (DB, env, URL)',
  'Cleanup auto dès la fermeture',
  'Sur ton serveur, tes règles'
]

export default function Solution() {
  const flowRef = useRef(null)

  useEffect(() => {
    const el = flowRef.current
    if (!el) return
    const cards = el.querySelectorAll('.step')
    const lines = el.querySelectorAll('.step-line')

    gsap.set(cards, { y: 40, opacity: 0 })
    gsap.set(lines, { scaleX: 0, transformOrigin: 'left center' })

    ScrollTrigger.create({
      trigger: el, start: 'top 75%', once: true,
      onEnter: () => {
        const tl = gsap.timeline()
        tl.to(cards, { y: 0, opacity: 1, duration: 0.8, ease: 'expo.out', stagger: 0.18 })
        tl.to(lines, { scaleX: 1, duration: 0.6, ease: 'power3.inOut', stagger: 0.18 }, 0.2)
      }
    })
  }, [])

  return (
    <Section id="solution" label="[02] LA SOLUTION">
      <RevealText as="h2" className="display" style={{
        fontSize: 'clamp(36px, 6vw, 80px)', maxWidth: 1100, marginBottom: 100, fontWeight: 700
      }}>
        Push ta branche. Hatch <span className="italic-accent" style={{ fontSize: 'inherit' }}>éclot.</span> Tout le monde teste.
      </RevealText>

      <div ref={flowRef} style={{
        display: 'grid', gridTemplateColumns: '1fr auto 1fr auto 1fr',
        alignItems: 'center', gap: 16, marginBottom: 100
      }}>
        {steps.map((s, i) => (
          <FlowStep key={s.label} s={s} showLine={i < steps.length - 1} />
        ))}
      </div>

      <div style={{
        display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 16
      }}>
        {bullets.map((b) => (
          <div key={b} style={{
            display: 'flex', alignItems: 'flex-start', gap: 12,
            padding: '18px 20px', borderLeft: '2px solid var(--accent)',
            background: 'rgba(255, 122, 61, 0.04)'
          }}>
            <span style={{ color: 'var(--accent)', fontFamily: 'var(--font-mono)' }}>→</span>
            <span style={{ fontSize: 15 }}>{b}</span>
          </div>
        ))}
      </div>

      <style>{`
        @media (max-width: 900px) {
          [class*="step"] { grid-template-columns: 1fr !important; }
          .step-line { display: none; }
        }
      `}</style>
    </Section>
  )
}

function FlowStep({ s, showLine }) {
  return (
    <>
      <div className="step" style={{
        padding: 32, background: 'var(--surface)',
        border: '1px solid var(--border)', borderRadius: 16,
        textAlign: 'center'
      }}>
        <StepIcon kind={s.icon} />
        <h4 className="display" style={{ fontSize: 22, fontWeight: 600, marginTop: 20, marginBottom: 8 }}>{s.label}</h4>
        <p style={{ color: 'var(--text-mute)', fontSize: 14 }}>{s.sub}</p>
      </div>
      {showLine && (
        <div className="step-line" style={{
          width: 40, height: 1, background: 'var(--accent)', position: 'relative'
        }}>
          <span style={{
            position: 'absolute', right: -6, top: -4, width: 0, height: 0,
            borderLeft: '8px solid var(--accent)',
            borderTop: '4px solid transparent', borderBottom: '4px solid transparent'
          }} />
        </div>
      )}
    </>
  )
}

function StepIcon({ kind }) {
  const stroke = 'var(--accent)'
  if (kind === 'push') return (
    <svg width="48" height="48" viewBox="0 0 48 48" style={{ margin: '0 auto' }}>
      <circle cx="14" cy="14" r="4" fill="none" stroke={stroke} strokeWidth="2"/>
      <circle cx="14" cy="34" r="4" fill="none" stroke={stroke} strokeWidth="2"/>
      <circle cx="34" cy="24" r="4" fill="none" stroke={stroke} strokeWidth="2"/>
      <path d="M14 18 L14 30 M16 30 L32 24 M16 18 L32 22" stroke={stroke} strokeWidth="2" fill="none"/>
    </svg>
  )
  if (kind === 'hatch') return (
    <svg width="48" height="48" viewBox="0 0 48 48" style={{ margin: '0 auto' }}>
      <path d="M24 6 C 14 6, 8 18, 8 28 C 8 38, 15 42, 24 42 S 40 38, 40 28 C 40 18, 34 6, 24 6 Z" fill="none" stroke="var(--text)" strokeWidth="2"/>
      <path d="M10 24 L16 20 L20 24 L24 20 L28 24 L32 20 L38 24" stroke={stroke} strokeWidth="2.5" fill="none" strokeLinecap="round"/>
    </svg>
  )
  return (
    <svg width="48" height="48" viewBox="0 0 48 48" style={{ margin: '0 auto' }}>
      <path d="M14 20 L14 8 L26 14 L20 16 L26 28 L22 30 L16 18 L14 20 Z" fill="none" stroke={stroke} strokeWidth="2" strokeLinejoin="round"/>
      <circle cx="32" cy="32" r="8" fill="none" stroke="var(--text)" strokeWidth="2"/>
    </svg>
  )
}
