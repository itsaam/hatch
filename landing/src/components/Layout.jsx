import { useEffect } from 'react'
import Lenis from 'lenis'
import Cursor from './Cursor.jsx'
import TopBar from './TopBar.jsx'

export default function Layout({ ready, children }) {
  useEffect(() => {
    if (!ready) return
    const lenis = new Lenis({ duration: 1.2, smoothWheel: true })
    let raf
    const tick = (t) => { lenis.raf(t); raf = requestAnimationFrame(tick) }
    raf = requestAnimationFrame(tick)
    return () => { cancelAnimationFrame(raf); lenis.destroy() }
  }, [ready])

  return (
    <>
      <Cursor />
      <TopBar />
      <main>{children}</main>
    </>
  )
}
