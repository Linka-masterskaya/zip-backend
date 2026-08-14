#!/usr/bin/env bash
set -euo pipefail

REPO="Linka-masterskaya/zip-backend"
SPIKE_ISSUE=110
DIR="$(cd "$(dirname "$0")" && pwd)"
ISSUE_FILE="$DIR/FOLLOW-UP-ISSUE.md"
TITLE="$(sed -n '1s/^# //p' "$ISSUE_FILE")"

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required: https://cli.github.com/" >&2
  exit 2
fi

gh auth status >/dev/null

existing_number="$(gh issue list \
  --repo "$REPO" \
  --state all \
  --limit 200 \
  --json number,title \
  --jq ".[] | select(.title == \"$TITLE\") | .number" | head -n 1)"

if [[ -n "$existing_number" ]]; then
  follow_up_number="$existing_number"
  echo "Follow-up issue already exists: #$follow_up_number"
else
  body_file="$(mktemp)"
  trap 'rm -f "$body_file"' EXIT
  tail -n +2 "$ISSUE_FILE" | sed '1{/^[[:space:]]*$/d;}' > "$body_file"

  follow_up_url="$(gh issue create \
    --repo "$REPO" \
    --title "$TITLE" \
    --body-file "$body_file")"
  follow_up_number="${follow_up_url##*/}"
  echo "Created follow-up issue: #$follow_up_number"

  # Metadata is useful but not part of the N5 acceptance criterion. Do not fail
  # closure if repository policy prevents one of these optional edits.
  gh issue edit "$follow_up_number" --repo "$REPO" --add-label archive --add-label pack >/dev/null 2>&1 || true
  gh issue edit "$follow_up_number" --repo "$REPO" --add-assignee AndreyGomzikov >/dev/null 2>&1 || true
fi

comment="N5 compatibility spike completed. Linka Config 2.0 is not compatible as-is with Linka Looks 3.2.10; a versioned converter/export mode is required. Follow-up implementation issue: #${follow_up_number}. Reproducible fixtures, runtime evidence and the compatibility matrix are under docs/compatibility/linka-looks/."

gh issue comment "$SPIKE_ISSUE" --repo "$REPO" --body "$comment" >/dev/null

gh issue close "$SPIKE_ISSUE" --repo "$REPO" --reason completed >/dev/null

echo "N5 closed: #$SPIKE_ISSUE -> follow-up #$follow_up_number"
