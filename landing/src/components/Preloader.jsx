import { useEffect, useRef, useState } from 'react'
import gsap from 'gsap'

export default function Preloader({ onDone }) {
  const root = useRef(null)
  const [hidden, setHidden] = useState(false)

  useEffect(() => {
    const tl = gsap.timeline({
      onComplete: () => {
        setHidden(true)
        onDone?.()
      }
    })
    tl.to('.egg-top', { y: -28, rotate: -8, duration: 0.7, ease: 'power3.inOut' }, 0.4)
      .to('.egg-bot', { y: 28, rotate: 6, duration: 0.7, ease: 'power3.inOut' }, 0.4)
      .to('.egg-top, .egg-bot', { opacity: 0, duration: 0.4 }, 0.9)
      .from('.egg-word', { opacity: 0, y: 20, duration: 0.6, ease: 'power3.out' }, 0.85)
      .to(root.current, { opacity: 0, duration: 0.5, ease: 'power2.inOut', delay: 0.3 })
  }, [onDone])

  if (hidden) return null

  return (
    <div ref={root} style={{
      position: 'fixed', inset: 0, zIndex: 10001, background: 'var(--bg)',
      display: 'grid', placeItems: 'center'
    }}>
      <div style={{ position: 'relative', width: 220, height: 160, display: 'grid', placeItems: 'center' }}>
        <svg className="egg-top" width="120" height="80" viewBox="0 0 120 80" style={{ position: 'absolute', top: 0 }}>
          <path d="M10 78 C 18 30, 42 4, 60 4 S 102 30, 110 78 L 100 70 L 88 78 L 76 70 L 64 78 L 52 70 L 40 78 L 28 70 L 18 78 Z"
            fill="#F5EFE6" />
        </svg>
        <svg className="egg-bot" width="120" height="80" viewBox="0 0 120 80" style={{ position: 'absolute', bottom: 0 }}>
          <path d="M10 2 L18 10 L28 2 L40 10 L52 2 L64 10 L76 2 L88 10 L100 2 L110 2 C 110 50, 90 78, 60 78 S 10 50, 10 2 Z"
            fill="#F5EFE6" />
        </svg>
        <span className="egg-word" style={{
          fontFamily: 'var(--font-display)', fontWeight: 800, fontSize: 56,
          letterSpacing: '-0.04em', color: 'var(--accent)',
          fontVariationSettings: "'opsz' 96"
        }}>HATCH</span>
      </div>
    </div>
  )
}
