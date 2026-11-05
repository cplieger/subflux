#!/usr/bin/env bash
# Apply the issue plan `release-patterns watch` wrote: create, reopen and
# edit issues with gh. Every value comes from the plan file, never from the
# command line, so upstream text cannot reach a shell word.
set -euo pipefail

PLAN="${1:?usage: apply-issues.sh actions.json}"
REPO="${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY}"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
jq -r '[.[].labels // [] | .[]] | unique | .[]' "$PLAN" >"$WORK/labels"
while IFS= read -r label; do
  gh label create "$label" --repo "$REPO" --force --color ededed >/dev/null
done <"$WORK/labels"

count=$(jq 'length' "$PLAN")
for ((i = 0; i < count; i++)); do
  jq -r ".[$i].body" "$PLAN" >"$WORK/body.md"
  kind=$(jq -r ".[$i].kind" "$PLAN")
  number=$(jq -r ".[$i].number // 0" "$PLAN")
  title=$(jq -r ".[$i].title" "$PLAN")
  labels=$(jq -r ".[$i].labels // [] | join(\",\")" "$PLAN")
  case "$kind" in
    create)
      gh issue create --repo "$REPO" --title "$title" --body-file "$WORK/body.md" --label "$labels"
      ;;
    reopen)
      gh issue reopen "$number" --repo "$REPO"
      gh issue edit "$number" --repo "$REPO" --title "$title" --body-file "$WORK/body.md"
      ;;
    edit)
      gh issue edit "$number" --repo "$REPO" --title "$title" --body-file "$WORK/body.md"
      ;;
    *)
      echo "apply-issues: ERROR unknown action kind '${kind}'" >&2
      exit 1
      ;;
  esac
done
echo "apply-issues: applied ${count} action(s)"
