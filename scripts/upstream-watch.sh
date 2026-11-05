#!/usr/bin/env bash
# Plan and apply the upstream-watch issues: what changed in TRaSH, Sonarr
# and Radarr outside the entries internal/release imports. DRY_RUN=1 prints
# the plan instead of applying it. Needs GH_TOKEN and GITHUB_REPOSITORY.
set -euo pipefail

REPO="${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY}"
: "${GH_TOKEN:?set GH_TOKEN}"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
gh api --paginate "repos/$REPO/issues?labels=upstream-watch&state=all&per_page=100" \
  --jq '.[] | select(.pull_request == null) | {number, state, title, body: (.body // "")}' >"$WORK/existing.jsonl"
go run ./cmd/release-patterns watch -dir internal/release -existing "$WORK/existing.jsonl" -out "$WORK/actions.json"
if [ "${DRY_RUN:-0}" = "1" ]; then
  jq . "$WORK/actions.json"
  exit 0
fi
bash scripts/apply-issues.sh "$WORK/actions.json"
