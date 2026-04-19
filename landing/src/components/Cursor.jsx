import { useEffect, useRef } from 'react'

export default function Cursor() {
  const ref = useRef(null)
  const target = useRef({ x: 0, y: 0 })
  const pos = useRef({ x: 0, y: 0 })

  useEffect(() => {
    if (window.matchMedia('(max-width: 900px)').matches) return
    const el = ref.current
    if (!el) return

    const onMove = (e) => { target.current.x = e.clientX; target.current.y = e.clientY }
    const onOver = (e) => {
      const t = e.target.closest('a, button, input')
      el.dataset.hover = t ? 'true' : 'false'
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseover', onOver)

    let raf
    const tick = () => {
      pos.current.x += (target.current.x - pos.current.x) * 0.18
      pos.current.y += (target.current.y - pos.current.y) * 0.18
      el.style.transform = `translate3d(${pos.current.x - 8}px, ${pos.current.y - 8}px, 0)`
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => { cancelAnimationFrame(raf); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseover', onOver) }
  }, [])

  return (
    <>
      <div
        ref={ref}
        style={{
          position: 'fixed', top: 0, left: 0, width: 16, height: 16,
          border: '1.5px solid var(--text)', borderRadius: '50%',
          pointerEvents: 'none', zIndex: 10000, mixBlendMode: 'difference',
          transition: 'width .25s var(--ease), height .25s var(--ease), border-color .25s var(--ease)'
        }}
      />
      <style>{`
        @media (max-width: 900px) { div[style*="z-index: 10000"] { display: none; } }
        div[data-hover="true"] {
          width: 36px !important; height: 36px !important;
          border-color: var(--accent) !important;
        }
      `}</style>
    </>
  )
}
