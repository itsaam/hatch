import { useState } from 'react'

export default function EmailCapture({ size = 'md' }) {
  const [email, setEmail] = useState('')
  const [state, setState] = useState('idle')

  const onSubmit = (e) => {
    e.preventDefault()
    if (!email || !email.includes('@')) return
    setState('loading')
    setTimeout(() => setState('success'), 700)
  }

  const big = size === 'lg'

  if (state === 'success') {
    return (
      <div style={{
        padding: big ? '20px 24px' : '14px 18px',
        border: '1px solid var(--border-accent)',
        borderRadius: 12, background: 'rgba(255, 122, 61, 0.08)',
        color: 'var(--accent)', fontFamily: 'var(--font-mono)',
        fontSize: 13, letterSpacing: '0.06em',
        display: 'flex', alignItems: 'center', gap: 10
      }}>
        <span>✓</span> Inscrit. On te prévient au lancement.
      </div>
    )
  }

  return (
    <form onSubmit={onSubmit} style={{
      display: 'flex', gap: 8,
      padding: 6, borderRadius: 14,
      border: '1px solid var(--border-strong)',
      background: 'var(--surface)',
      maxWidth: big ? 540 : 460
    }}>
      <input
        type="email"
        placeholder="ton@email.dev"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        required
        style={{
          flex: 1, padding: big ? '16px 14px' : '12px 12px',
          fontSize: big ? 16 : 14, color: 'var(--text)'
        }}
      />
      <button type="submit" disabled={state === 'loading'} style={{
        padding: big ? '16px 22px' : '12px 18px',
        background: 'var(--accent)', color: '#0F0D0A',
        borderRadius: 10, fontWeight: 600, fontSize: big ? 15 : 13,
        letterSpacing: '-0.01em',
        transition: 'transform .25s var(--ease-bounce), box-shadow .25s var(--ease)',
        boxShadow: '0 0 0 0 rgba(255,122,61,0)'
      }}
        onMouseEnter={(e) => {
          e.currentTarget.style.transform = 'translateY(-2px)'
          e.currentTarget.style.boxShadow = '0 8px 32px rgba(255,122,61,0.4)'
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.transform = 'translateY(0)'
          e.currentTarget.style.boxShadow = '0 0 0 0 rgba(255,122,61,0)'
        }}
      >
        {state === 'loading' ? '...' : 'Prévenez-moi'}
      </button>
    </form>
  )
}
