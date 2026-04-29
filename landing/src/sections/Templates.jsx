import { useEffect, useRef } from 'react'
import gsap from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import Section from '../components/Section.jsx'
import RevealText from '../components/RevealText.jsx'

gsap.registerPlugin(ScrollTrigger)

const REPO_BASE = 'https://github.com/hatchpr'

const templates = [
  {
    name: 'nextjs-postgres',
    stack: 'Next.js 15 · Postgres 16',
    desc: 'App Router en standalone + DB isolée par PR. Idéal pour previewer une feature full-stack.'
  },
  {
    name: 'fastapi-postgres',
    stack: 'FastAPI · Postgres 16',
    desc: 'API Python async avec migrations et seed automatique au démarrage du preview.'
  },
  {
    name: 'django-redis',
    stack: 'Django 5 · Redis 7',
    desc: 'Backend Django classique avec cache et sessions Redis. Healthcheck inclus.'
  },
  {
    name: 'rails-sidekiq',
    stack: 'Rails 7 · Sidekiq · Redis',
    desc: 'API Rails avec workers Sidekiq pour les jobs en background, prêt à brancher.'
  },
  {
    name: 'static',
    stack: 'nginx alpine',
    desc: 'Site statique servi par nginx. Le plus rapide à booter, parfait pour des previews docs.'
  }
]

export default function Templates() {
  const ref = useRef(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const cards = el.querySelectorAll('.tpl-card')
    gsap.set(cards, { y: 32, opacity: 0 })

    ScrollTrigger.create({
      trigger: el, start: 'top 75%', once: true,
      onEnter: () => {
        gsap.to(cards, {
          y: 0, opacity: 1, duration: 0.7, ease: 'expo.out', stagger: 0.1
        })
      }
    })
  }, [])

  return (
    <Section id="templates" label="[03] LES TEMPLATES">
      <RevealText as="h2" className="display" style={{
        fontSize: 'clamp(32px, 5vw, 64px)', maxWidth: 900, marginBottom: 24, fontWeight: 700
      }}>
        Démarre en 30 secondes. <span className="italic-accent" style={{ fontSize: 'inherit' }}>Fork, push, preview.</span>
      </RevealText>

      <p style={{
        color: 'var(--text-mute)', fontSize: 17, maxWidth: 640, marginBottom: 64, lineHeight: 1.6
      }}>
        Cinq templates prêts à l'emploi pour les stacks les plus courantes. Chaque
        repo contient un <code style={{
          fontFamily: 'var(--font-mono)', color: 'var(--accent)',
          background: 'rgba(255,122,61,0.08)', padding: '2px 6px', borderRadius: 4, fontSize: 14
        }}>.hatch.yml</code> et un Dockerfile multi-stage optimisé.
      </p>

      <div ref={ref} className="tpl-grid" style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(3, 1fr)',
        gap: 16
      }}>
        {templates.map((t) => (
          <TemplateCard key={t.name} t={t} />
        ))}
      </div>

      <style>{`
        @media (max-width: 900px) {
          .tpl-grid { grid-template-columns: 1fr !important; }
        }
        @media (min-width: 901px) and (max-width: 1180px) {
          .tpl-grid { grid-template-columns: repeat(2, 1fr) !important; }
        }
        .tpl-card {
          transition: border-color 240ms ease, transform 240ms ease, background 240ms ease;
        }
        .tpl-card:hover {
          border-color: var(--accent) !important;
          transform: translateY(-2px);
          background: rgba(255, 122, 61, 0.03) !important;
        }
        .tpl-card:hover .tpl-cta {
          color: var(--accent);
        }
        .tpl-card:hover .tpl-cta-arrow {
          transform: translateX(4px);
        }
      `}</style>
    </Section>
  )
}

function TemplateCard({ t }) {
  const url = `${REPO_BASE}/template-${t.name}`
  return (
    <a
      className="tpl-card"
      href={url}
      target="_blank"
      rel="noreferrer"
      style={{
        display: 'flex', flexDirection: 'column',
        padding: 28,
        background: 'var(--surface)',
        border: '1px solid var(--border)',
        borderRadius: 16,
        textDecoration: 'none',
        color: 'inherit',
        minHeight: 240
      }}
    >
      <div className="mono" style={{
        color: 'var(--text-faint)', fontSize: 11,
        marginBottom: 14, letterSpacing: '0.08em'
      }}>
        ./{t.name}
      </div>

      <h3 className="display" style={{
        fontSize: 24, fontWeight: 600, margin: '0 0 8px',
        letterSpacing: '-0.01em'
      }}>
        {t.name}
      </h3>

      <div style={{
        fontFamily: 'var(--font-mono)', fontSize: 12,
        color: 'var(--accent)', marginBottom: 16
      }}>
        {t.stack}
      </div>

      <p style={{
        color: 'var(--text-mute)', fontSize: 14,
        lineHeight: 1.55, margin: '0 0 24px', flex: 1
      }}>
        {t.desc}
      </p>

      <div className="tpl-cta" style={{
        display: 'inline-flex', alignItems: 'center', gap: 8,
        fontFamily: 'var(--font-mono)', fontSize: 13,
        color: 'var(--text)',
        transition: 'color 200ms ease'
      }}>
        Utiliser ce template
        <span className="tpl-cta-arrow" style={{
          transition: 'transform 200ms ease',
          display: 'inline-block'
        }}>→</span>
      </div>
    </a>
  )
}
