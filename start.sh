#!/bin/bash

set -e

cd "$(dirname "$0")"

export PORT="${PORT:-4000}"

BIN="${BIN:-}"
if [ -z "$BIN" ]; then
  case "$(uname -m)" in
    aarch64|arm64) BIN="./llmproxy-arm64" ;;
    *) BIN="./llmproxy" ;;
  esac
fi

if [ ! -x "$BIN" ]; then
  echo "==> Binary not found: $BIN"
  exit 1
fi

PORT_NUM="$(grep -o '"listen"[[:space:]]*:[[:space:]]*"[^"]*"' ./config/config.json 2>/dev/null | sed 's/.*://; s/"//g; s/.*://' )"
PORT_NUM="${PORT_NUM:-$PORT}"

OLD_PIDS="$(lsof -ti tcp:$PORT_NUM || true)"
if [ -n "$OLD_PIDS" ]; then
  echo "==> Killing existing process on port $PORT_NUM..."
  kill -9 $OLD_PIDS
  sleep 1
fi

echo "==> Starting $BIN on port $PORT_NUM..."
nohup "$BIN" > /tmp/llmproxy.log 2>&1 &

if kill -0 $! > /dev/null 2>&1; then
  echo "==> Started. Logs: /tmp/llmproxy.log"
else
  echo "==> Failed to start. Logs: /tmp/llmproxy.log"
  exit 1
fi