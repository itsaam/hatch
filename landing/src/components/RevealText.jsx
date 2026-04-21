import { useEffect, useRef } from 'react'
import gsap from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'

gsap.registerPlugin(ScrollTrigger)

export default function RevealText({ children, as: Tag = 'h2', className, style, delay = 0, trigger = 'scroll' }) {
  const ref = useRef(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const words = el.querySelectorAll('.word > span')
    if (!words.length) return

    gsap.set(words, { yPercent: 110, opacity: 0 })

    const anim = () => gsap.to(words, {
      yPercent: 0, opacity: 1,
      duration: 1.1, ease: 'expo.out',
      stagger: 0.04, delay
    })

    if (trigger === 'load') {
      anim()
    } else {
      const st = ScrollTrigger.create({
        trigger: el, start: 'top 85%', once: true,
        onEnter: anim
      })
      return () => st.kill()
    }
  }, [delay, trigger])

  const renderChild = (node, key = 0) => {
    if (typeof node === 'string') {
      return node.split(' ').map((w, i) => (
        <span key={`${key}-${i}`} className="word" style={{ display: 'inline-block', overflow: 'hidden', paddingBottom: '0.12em', marginBottom: '-0.12em' }}>
          <span style={{ display: 'inline-block', willChange: 'transform' }}>{w}&nbsp;</span>
        </span>
      ))
    }
    if (Array.isArray(node)) return node.map((n, i) => renderChild(n, i))
    if (node && node.props && node.props.children) {
      const inner = renderChild(node.props.children, key)
      return { ...node, props: { ...node.props, children: inner } }
    }
    return node
  }

  return (
    <Tag ref={ref} className={className} style={style}>
      {renderChild(children)}
    </Tag>
  )
}
