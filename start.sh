#!/bin/bash

set -e

export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"

export NODE_ENV=production
export PORT="${PORT:-4000}"

echo "==> Building..."
npm run build


OLD_PIDS="$(lsof -ti tcp:$PORT || true)"
if [ -n "$OLD_PIDS" ]; then
  kill -9 $OLD_PIDS
echo "==> Killing existing process on port $PORT..." 
  sleep 1
fi

echo "==> Starting server..."
nohup npm run start > /tmp/llmproxy2.log 2>&1 &


if ps -p $! > /dev/null 2>&1; then
  echo "==> Started. Logs: /tmp/llmproxy2.log"
else
  echo "==> Failed to start. Logs: /tmp/llmproxy2.log"
  exit 1
fi
