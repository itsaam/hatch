import { Pool } from 'pg'

export const dynamic = 'force-dynamic'

let pool: Pool | null = null
function getPool() {
  if (!pool) {
    pool = new Pool({
      connectionString: process.env.DATABASE_URL,
      max: 4,
    })
  }
  return pool
}

export default async function DbCheck() {
  let result: { ok: boolean; value?: number; error?: string }

  try {
    const { rows } = await getPool().query('SELECT 1 AS value')
    result = { ok: true, value: rows[0].value }
  } catch (err) {
    result = { ok: false, error: err instanceof Error ? err.message : String(err) }
  }

  return (
    <main>
      <span className="badge">db · check</span>
      <h1>Postgres connectivity</h1>
      {result.ok ? (
        <>
          <p className="ok">Connection OK — SELECT 1 returned {result.value}.</p>
          <pre>{`SELECT 1\n=> ${result.value}`}</pre>
        </>
      ) : (
        <>
          <p className="error">Connection failed.</p>
          <pre>{result.error}</pre>
        </>
      )}
      <p><a href="/">← back</a></p>
    </main>
  )
}
