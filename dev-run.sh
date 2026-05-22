#!/bin/sh
# Build and run subflux locally in WSL for UI development.
#
# Setup (once):
#   cp dev-config.yaml dev-config.local.yaml
#   # Edit dev-config.local.yaml with real API keys
#
# Usage: bash dev-run.sh
#
# The UI is at http://localhost:8374
# Media paths won't resolve locally; search/download won't work
# but the UI, scoring, and API browsing all work fine.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOCAL_CFG="$SCRIPT_DIR/dev-config.local.yaml"

if [ ! -f "$LOCAL_CFG" ]; then
  printf 'Missing %s\n' "$LOCAL_CFG"
  printf 'Run: cp dev-config.yaml dev-config.local.yaml\n'
  printf 'Then edit it with your API keys.\n'
  exit 1
fi

printf 'Compiling TypeScript...\n'
cd "$SCRIPT_DIR/internal/server/static-src" || exit 1
tsgo

printf 'Building subflux...\n'
cd "$SCRIPT_DIR" || exit 1
GODEBUG=goindex=0 go build -o /tmp/subflux .

sudo mkdir -p /config
sudo cp "$LOCAL_CFG" /config/config.yaml

printf 'Starting subflux on http://localhost:8374\n'
printf 'Press Ctrl+C to stop\n'
exec /tmp/subflux
