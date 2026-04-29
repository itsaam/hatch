import os
import psycopg
from fastapi import FastAPI, HTTPException

DATABASE_URL = os.environ.get("DATABASE_URL", "")

app = FastAPI(title="hatch-fastapi-postgres")


@app.get("/")
def root():
    return {"status": "ok", "service": "fastapi-postgres"}


@app.get("/health")
def health():
    return {"status": "ok"}


@app.get("/db-check")
def db_check():
    if not DATABASE_URL:
        raise HTTPException(500, "DATABASE_URL not set")
    try:
        with psycopg.connect(DATABASE_URL, connect_timeout=5) as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT 1")
                one = cur.fetchone()[0]
                cur.execute("SELECT count(*) FROM preview_seed")
                count = cur.fetchone()[0]
        return {"db": "ok", "select_1": one, "preview_seed_rows": count}
    except Exception as e:
        raise HTTPException(500, f"db error: {e}")
