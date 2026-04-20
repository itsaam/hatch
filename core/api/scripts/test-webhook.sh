#!/usr/bin/env bash
set -euo pipefail

ACTION=${1:-opened}
PR=${2:-42}
REPO=${3:-itsaam/hatch}
BRANCH=${4:-feat/demo}
SHA=${5:-abcdef1234567890abcdef1234567890abcdef12}

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
SECRET=$(grep '^GITHUB_WEBHOOK_SECRET=' "$ROOT/.env" | cut -d= -f2)

PAYLOAD=$(cat <<JSON
{"action":"$ACTION","number":$PR,"pull_request":{"head":{"ref":"$BRANCH","sha":"$SHA"}},"repository":{"full_name":"$REPO"}}
JSON
)

HEX=$(printf '%s' "$PAYLOAD" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.*= *//')

echo "==> $ACTION $REPO#$PR"
curl -s -w "\nHTTP %{http_code}\n" -X POST http://localhost:8080/api/github/webhook \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: pull_request" \
  -H "X-Hub-Signature-256: sha256=$HEX" \
  -d "$PAYLOAD"
