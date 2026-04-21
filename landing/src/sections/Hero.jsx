import { useEffect, useRef, useState } from 'react'
import gsap from 'gsap'
import EmailCapture from '../components/EmailCapture.jsx'
import Marquee from '../components/Marquee.jsx'

export default function Hero({ ready }) {
  const titleRef = useRef(null)
  const subRef = useRef(null)
  const [count, setCount] = useState(null)

  useEffect(() => {
    const apiUrl = import.meta.env.VITE_API_URL || 'http://localhost:8080'
    fetch(`${apiUrl}/api/subscribers/count`)
      .then((r) => r.ok ? r.json() : null)
      .then((d) => { if (d && typeof d.count === 'number') setCount(d.count) })
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (!ready || !titleRef.current) return

    const ctx = gsap.context(() => {
      const words = titleRef.current.querySelectorAll('.w > span')
      if (!words.length) return
      gsap.set(words, { yPercent: 110, opacity: 0 })
      gsap.set([subRef.current, '.hero-form', '.hero-meta', '.hero-tags'], { y: 24, opacity: 0 })

      const tl = gsap.timeline({ delay: 0.2 })
      tl.to(words, { yPercent: 0, opacity: 1, duration: 1.2, ease: 'expo.out', stagger: 0.05 })
        .to(subRef.current, { y: 0, opacity: 1, duration: 0.9, ease: 'expo.out' }, '-=0.6')
        .to('.hero-form', { y: 0, opacity: 1, duration: 0.7, ease: 'expo.out' }, '-=0.5')
        .to('.hero-meta', { y: 0, opacity: 1, duration: 0.6, ease: 'expo.out' }, '-=0.4')
        .to('.hero-tags', { y: 0, opacity: 1, duration: 0.6, ease: 'expo.out' }, '-=0.4')
    }, titleRef)

    return () => ctx.revert()
  }, [ready])

  const words = ['Chaque', 'PR', 'éclot', 'en', 'preview', 'live,']
  return (
    <section style={{
      minHeight: '100vh', display: 'flex', flexDirection: 'column',
      justifyContent: 'center', padding: '120px var(--pad) 60px',
      position: 'relative', overflow: 'hidden'
    }}>
      <div style={{
        position: 'absolute', top: '20%', right: '-10%', width: 500, height: 500,
        background: 'radial-gradient(circle, rgba(255, 122, 61, 0.15) 0%, transparent 60%)',
        filter: 'blur(60px)', pointerEvents: 'none'
      }} />
      <div className="container" style={{ textAlign: 'center', position: 'relative' }}>
        <h1 ref={titleRef} className="display" style={{
          fontSize: 'clamp(48px, 9vw, 128px)',
          fontWeight: 800, maxWidth: 1100, margin: '0 auto'
        }}>
          {words.map((w, i) => (
            <span key={i} className="w" style={{ display: 'inline-block', overflow: 'hidden', paddingBottom: '0.12em', marginBottom: '-0.12em' }}>
              <span style={{ display: 'inline-block', willChange: 'transform' }}>{w}&nbsp;</span>
            </span>
          ))}
          <span className="w" style={{ display: 'inline-block', overflow: 'hidden', paddingBottom: '0.12em', marginBottom: '-0.12em' }}>
            <span className="italic-accent" style={{ display: 'inline-block', willChange: 'transform', fontSize: 'clamp(48px, 9vw, 128px)' }}>instantanément.</span>
          </span>
        </h1>

        <p ref={subRef} style={{
          marginTop: 32, fontSize: 'clamp(16px, 1.4vw, 19px)',
          color: 'var(--text-mute)', maxWidth: 620, marginInline: 'auto',
          lineHeight: 1.5
        }}>
          L'outil open-source de PR preview environments.<br />
          Sur ton infra. Sans Vercel. Sans Coolify.
        </p>

        <div className="hero-form" style={{ marginTop: 40, display: 'grid', placeItems: 'center' }}>
          <EmailCapture size="lg" />
        </div>

        <p className="hero-meta mono" style={{
          marginTop: 18, color: 'var(--text-faint)'
        }}>
          ≈ {count ?? '—'} {count === 1 ? 'dev déjà inscrit' : 'devs déjà inscrits'}
        </p>

        <div className="hero-tags" style={{
          marginTop: 60, display: 'flex', gap: 12, justifyContent: 'center', flexWrap: 'wrap'
        }}>
          <Tag>FREE & OPEN SOURCE</Tag>
          <span className="mono" style={{ color: 'var(--text-faint)' }}>·</span>
          <Tag>BIENTÔT EN BETA</Tag>
        </div>
      </div>

      <div style={{ marginTop: 'auto', paddingTop: 80 }}>
        <Marquee items={['DOCKER', 'GITHUB WEBHOOKS', 'TRAEFIK', 'SELF-HOSTED', 'OPEN SOURCE', 'PREVIEW DEPLOYMENTS', 'ZERO CONFIG']} />
      </div>
    </section>
  )
}

function Tag({ children }) {
  return (
    <span className="mono" style={{
      padding: '6px 12px', border: '1px solid var(--border-strong)',
      borderRadius: 999, color: 'var(--text)'
    }}>[{children}]</span>
  )
}
