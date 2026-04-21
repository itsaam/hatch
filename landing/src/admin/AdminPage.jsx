import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080'
const POLL_MS = 5000
const LOGS_POLL_MS = 2500
const TOKEN_KEY = 'hatch_admin_token'

const statusColor = {
  building: 'var(--accent-2)',
  deployed: '#3DD68C',
  failed: '#FF5E5E',
  closed: 'var(--text-faint)',
  expired: 'var(--text-faint)',
}

const useToken = () => {
  const [token, setToken] = useState(() =>
    typeof window === 'undefined' ? '' : localStorage.getItem(TOKEN_KEY) || ''
  )
  const save = (v) => {
    localStorage.setItem(TOKEN_KEY, v)
    setToken(v)
  }
  const clear = () => {
    localStorage.removeItem(TOKEN_KEY)
    setToken('')
  }
  return { token, save, clear }
}

async function apiFetch(token, path, opts = {}) {
  const res = await fetch(`${API_URL}${path}`, {
    ...opts,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(opts.headers || {}),
    },
  })
  if (res.status === 401 || res.status === 403) {
    throw new Error('unauthorized')
  }
  const text = await res.text()
  try {
    return { ok: res.ok, status: res.status, data: JSON.parse(text) }
  } catch {
    return { ok: res.ok, status: res.status, data: text }
  }
}

function TokenGate({ onReady }) {
  const [value, setValue] = useState('')
  return (
    <main style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', padding: 24 }}>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (value.trim()) onReady(value.trim())
        }}
        style={{
          width: '100%', maxWidth: 420, border: '1px solid var(--border)',
          background: 'var(--surface)', borderRadius: 16, padding: 32,
          display: 'flex', flexDirection: 'column', gap: 20,
        }}
      >
        <div>
          <div style={{ fontFamily: 'var(--font-display)', fontSize: 28, fontWeight: 600 }}>
            hatch · admin
          </div>
          <div style={{ color: 'var(--text-mute)', fontSize: 13, marginTop: 4 }}>
            Colle ton HATCH_ADMIN_TOKEN pour accéder au dashboard.
          </div>
        </div>
        <input
          type="password"
          autoFocus
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="token…"
          style={{
            padding: '12px 14px', borderRadius: 10,
            border: '1px solid var(--border-strong)',
            background: 'var(--bg)', color: 'var(--text)',
            fontFamily: 'var(--font-mono)', fontSize: 13,
          }}
        />
        <button
          type="submit"
          style={{
            padding: '12px 14px', borderRadius: 10, border: 'none',
            background: 'var(--accent)', color: '#0F0D0A',
            fontWeight: 600, cursor: 'pointer',
          }}
        >
          Entrer
        </button>
      </form>
    </main>
  )
}

function StatusPill({ status }) {
  const color = statusColor[status] || 'var(--text-mute)'
  const pulsing = status === 'building'
  return (
    <span
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 8,
        padding: '4px 10px', borderRadius: 999,
        border: `1px solid ${color}`,
        color,
        fontFamily: 'var(--font-mono)', fontSize: 11,
        textTransform: 'uppercase', letterSpacing: 0.5,
      }}
    >
      <span
        style={{
          width: 6, height: 6, borderRadius: '50%', background: color,
          animation: pulsing ? 'pulseDot 1.2s infinite' : 'none',
        }}
      />
      {status}
    </span>
  )
}

function relative(iso) {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h`
  return `${Math.floor(h / 24)}j`
}

function LogsModal({ token, preview, onClose }) {
  const [state, setState] = useState({ loading: true, data: null, error: '' })
  const timerRef = useRef(null)
  const [owner, repo] = preview.repo_full_name.split('/')

  const fetchLogs = useCallback(async () => {
    try {
      const { ok, data } = await apiFetch(
        token,
        `/api/previews/${owner}/${repo}/${preview.pr_number}/logs`,
      )
      if (!ok) {
        setState({ loading: false, data: null, error: (data && data.error) || 'err' })
      } else {
        setState({ loading: false, data, error: '' })
      }
    } catch (e) {
      setState({ loading: false, data: null, error: String(e.message || e) })
    }
  }, [token, owner, repo, preview.pr_number])

  useEffect(() => {
    fetchLogs()
    timerRef.current = setInterval(fetchLogs, LOGS_POLL_MS)
    return () => clearInterval(timerRef.current)
  }, [fetchLogs])

  const content = () => {
    if (state.loading && !state.data) {
      return <div style={{ color: 'var(--text-mute)' }}>Chargement…</div>
    }
    if (state.error && !state.data) {
      return <div style={{ color: '#FF5E5E' }}>Erreur : {state.error}</div>
    }
    const { data } = state
    if (data?.kind === 'runtime') {
      return (
        <pre style={preStyle}>{data.logs || '(pas de logs)'}</pre>
      )
    }
    if (data?.kind === 'build') {
      return (data.services || []).map((svc) => (
        <div key={svc.service} style={{ marginBottom: 20 }}>
          <div style={{
            display: 'flex', justifyContent: 'space-between', alignItems: 'center',
            marginBottom: 8,
          }}>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text)' }}>
              {svc.service} · <span style={{ color: statusColor[svc.status === 'success' ? 'deployed' : svc.status] || 'var(--text-mute)' }}>{svc.status}</span>
            </div>
            <div style={{ color: 'var(--text-faint)', fontSize: 11 }}>
              démarré il y a {relative(svc.started_at)}
              {svc.completed_at ? ` · terminé il y a ${relative(svc.completed_at)}` : ''}
            </div>
          </div>
          {svc.error && (
            <div style={{
              color: '#FF5E5E', fontFamily: 'var(--font-mono)', fontSize: 12,
              padding: 10, border: '1px solid rgba(255,94,94,0.3)',
              borderRadius: 6, marginBottom: 8, background: 'rgba(255,94,94,0.05)',
            }}>
              {svc.error}
            </div>
          )}
          <pre style={preStyle}>{svc.output || '(stream vide)'}</pre>
        </div>
      ))
    }
    return <div style={{ color: 'var(--text-mute)' }}>Pas de logs disponibles.</div>
  }

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.7)',
        backdropFilter: 'blur(4px)', zIndex: 50, display: 'grid',
        placeItems: 'center', padding: 24,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 'min(1100px, 100%)', maxHeight: '85vh',
          background: 'var(--surface)', border: '1px solid var(--border-strong)',
          borderRadius: 16, padding: 24, display: 'flex', flexDirection: 'column',
        }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <div>
            <div style={{ fontFamily: 'var(--font-display)', fontSize: 20, fontWeight: 600 }}>
              {preview.repo_full_name} #{preview.pr_number}
            </div>
            <div style={{ color: 'var(--text-mute)', fontSize: 12 }}>
              {preview.branch} · {preview.commit_sha?.slice(0, 7)}
            </div>
          </div>
          <button
            onClick={onClose}
            style={{
              padding: '8px 14px', borderRadius: 8,
              border: '1px solid var(--border-strong)',
              background: 'transparent', color: 'var(--text)',
              cursor: 'pointer', fontSize: 13,
            }}
          >
            Fermer
          </button>
        </div>
        <div style={{ overflow: 'auto', flex: 1 }}>{content()}</div>
      </div>
    </div>
  )
}

const preStyle = {
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 8,
  padding: 12,
  color: 'var(--text)',
  fontFamily: 'var(--font-mono)',
  fontSize: 12,
  lineHeight: 1.5,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
  maxHeight: 400,
  overflow: 'auto',
  margin: 0,
}

function Dashboard({ token, onLogout }) {
  const [previews, setPreviews] = useState([])
  const [err, setErr] = useState('')
  const [logsFor, setLogsFor] = useState(null)
  const [pending, setPending] = useState({}) // `${repo}#${pr}:${action}` -> true
  const timerRef = useRef(null)
  const firstLoadRef = useRef(true)

  const refresh = useCallback(async () => {
    try {
      const { ok, data } = await apiFetch(token, '/api/previews')
      if (!ok) {
        if ((data && data.error) === 'unauthorized') throw new Error('unauthorized')
        setErr((data && data.error) || 'erreur')
        return
      }
      setPreviews(Array.isArray(data) ? data : [])
      setErr('')
      firstLoadRef.current = false
    } catch (e) {
      setErr(String(e.message || e))
      if (e.message === 'unauthorized') onLogout()
    }
  }, [token, onLogout])

  useEffect(() => {
    refresh()
    timerRef.current = setInterval(refresh, POLL_MS)
    const onVis = () => {
      if (document.visibilityState === 'visible') refresh()
    }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      clearInterval(timerRef.current)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [refresh])

  const doAction = async (p, action) => {
    const [owner, repo] = p.repo_full_name.split('/')
    const key = `${p.repo_full_name}#${p.pr_number}:${action}`
    if (action === 'destroy' &&
      !confirm(`Destroy preview ${p.repo_full_name}#${p.pr_number} ?`)) return
    setPending((s) => ({ ...s, [key]: true }))
    try {
      const method = action === 'redeploy' ? 'POST' : 'DELETE'
      const path =
        action === 'redeploy'
          ? `/api/previews/${owner}/${repo}/${p.pr_number}/redeploy`
          : `/api/previews/${owner}/${repo}/${p.pr_number}`
      await apiFetch(token, path, { method })
      refresh()
    } finally {
      setPending((s) => {
        const next = { ...s }
        delete next[key]
        return next
      })
    }
  }

  const sorted = useMemo(
    () => [...previews].sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    [previews],
  )
  const counts = useMemo(() => {
    const c = { building: 0, deployed: 0, failed: 0, total: previews.length }
    for (const p of previews) c[p.status] = (c[p.status] || 0) + 1
    return c
  }, [previews])

  return (
    <main style={{
      minHeight: '100vh',
      padding: '40px clamp(20px, 4vw, 56px)',
      maxWidth: 1400, margin: '0 auto',
    }}>
      <header style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end',
        marginBottom: 32, gap: 24, flexWrap: 'wrap',
      }}>
        <div>
          <div style={{ fontFamily: 'var(--font-display)', fontSize: 40, fontWeight: 700, letterSpacing: -0.5 }}>
            Previews
          </div>
          <div style={{ color: 'var(--text-mute)', fontSize: 13, marginTop: 4 }}>
            {counts.total} total · {counts.deployed || 0} live · {counts.building || 0} en cours · {counts.failed || 0} échec
          </div>
        </div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <span style={{ fontSize: 11, color: 'var(--text-faint)', fontFamily: 'var(--font-mono)' }}>
            refresh · {POLL_MS / 1000}s
          </span>
          <button
            onClick={onLogout}
            style={{
              padding: '8px 14px', borderRadius: 8,
              border: '1px solid var(--border-strong)',
              background: 'transparent', color: 'var(--text)',
              cursor: 'pointer', fontSize: 13,
            }}
          >
            Logout
          </button>
        </div>
      </header>

      {err && (
        <div style={{
          padding: 12, borderRadius: 10, background: 'rgba(255,94,94,0.06)',
          border: '1px solid rgba(255,94,94,0.25)', color: '#FF9A9A',
          marginBottom: 16, fontSize: 13,
        }}>{err}</div>
      )}

      <div style={{
        border: '1px solid var(--border)', borderRadius: 16,
        background: 'var(--surface)', overflow: 'hidden',
      }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr style={{ background: 'var(--surface-2)', color: 'var(--text-mute)' }}>
              <Th>Repo · PR</Th>
              <Th>Branch</Th>
              <Th>SHA</Th>
              <Th>Status</Th>
              <Th>Updated</Th>
              <Th style={{ textAlign: 'right' }}>Actions</Th>
            </tr>
          </thead>
          <tbody>
            {sorted.length === 0 && (
              <tr><td colSpan={6} style={{ padding: 40, textAlign: 'center', color: 'var(--text-faint)' }}>
                {firstLoadRef.current ? 'Chargement…' : 'Aucune preview.'}
              </td></tr>
            )}
            {sorted.map((p) => {
              const [owner, repo] = p.repo_full_name.split('/')
              const k = `${p.repo_full_name}#${p.pr_number}`
              return (
                <tr key={k} style={{ borderTop: '1px solid var(--border)', transition: 'background 0.3s var(--ease)' }}>
                  <Td>
                    <div style={{ fontWeight: 600 }}>{p.repo_full_name}</div>
                    <div style={{ color: 'var(--text-mute)', fontSize: 11, marginTop: 2 }}>#{p.pr_number}</div>
                  </Td>
                  <Td><code style={codeStyle}>{p.branch}</code></Td>
                  <Td><code style={codeStyle}>{p.commit_sha?.slice(0, 7)}</code></Td>
                  <Td><StatusPill status={p.status} /></Td>
                  <Td style={{ color: 'var(--text-mute)' }}>il y a {relative(p.updated_at)}</Td>
                  <Td style={{ textAlign: 'right' }}>
                    <div style={{ display: 'inline-flex', gap: 6 }}>
                      {p.url && (
                        <ActionButton href={p.url} target="_blank">Open</ActionButton>
                      )}
                      <ActionButton onClick={() => setLogsFor(p)}>Logs</ActionButton>
                      <ActionButton
                        onClick={() => doAction(p, 'redeploy')}
                        disabled={!!pending[`${k}:redeploy`]}
                      >
                        {pending[`${k}:redeploy`] ? '…' : 'Redeploy'}
                      </ActionButton>
                      <ActionButton
                        tone="danger"
                        onClick={() => doAction(p, 'destroy')}
                        disabled={!!pending[`${k}:destroy`]}
                      >
                        {pending[`${k}:destroy`] ? '…' : 'Destroy'}
                      </ActionButton>
                    </div>
                  </Td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {logsFor && <LogsModal token={token} preview={logsFor} onClose={() => setLogsFor(null)} />}

      <style>{`
        @keyframes pulseDot {
          0%,100% { opacity: 1; }
          50%     { opacity: 0.3; }
        }
      `}</style>
    </main>
  )
}

const thTdBase = {
  padding: '14px 16px',
  textAlign: 'left',
  fontWeight: 500,
  verticalAlign: 'middle',
}
const Th = ({ children, style }) => (
  <th style={{ ...thTdBase, fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.6, ...style }}>{children}</th>
)
const Td = ({ children, style }) => (
  <td style={{ ...thTdBase, color: 'var(--text)', ...style }}>{children}</td>
)
const codeStyle = {
  fontFamily: 'var(--font-mono)', fontSize: 11,
  padding: '2px 6px', borderRadius: 4,
  background: 'var(--bg)', color: 'var(--text-mute)',
  border: '1px solid var(--border)',
}

function ActionButton({ children, tone, ...rest }) {
  const isDanger = tone === 'danger'
  const style = {
    padding: '6px 10px', borderRadius: 6,
    border: `1px solid ${isDanger ? 'rgba(255,94,94,0.35)' : 'var(--border-strong)'}`,
    background: 'transparent',
    color: isDanger ? '#FF9A9A' : 'var(--text)',
    fontSize: 11, fontWeight: 500, cursor: 'pointer',
    textDecoration: 'none', display: 'inline-block',
    transition: 'all 0.15s var(--ease)',
  }
  if (rest.href) return <a {...rest} style={style}>{children}</a>
  return <button {...rest} style={{ ...style, opacity: rest.disabled ? 0.5 : 1 }}>{children}</button>
}

export default function AdminPage() {
  const { token, save, clear } = useToken()
  if (!token) return <TokenGate onReady={save} />
  return <Dashboard token={token} onLogout={clear} />
}
