#!/usr/bin/env bash
# Release gate: stop unless the commit being released passed its checks.
#
#   .github/scripts/require-green.sh [commit]
#
# Looks at the GitHub Actions runs for the commit (GITHUB_SHA by default) that
# came from a push or a merge queue, such as CI on main, and takes each
# workflow's newest run. Any that failed, was cancelled or timed out stops the
# release; any still running is waited for, up to REQUIRE_GREEN_WAIT_MINUTES
# (default 45). The workflow running this script is left out, so a release
# run again by hand isn't held by its own earlier attempt. A commit with no
# such runs (only prose changed, so CI was skipped) passes.
#
# Needs GH_TOKEN with actions: read.
set -euo pipefail

repo="${GITHUB_REPOSITORY:?}"
sha="${1:-${GITHUB_SHA:?}}"
wait_minutes="${REQUIRE_GREEN_WAIT_MINUTES:-45}"
own_workflow="$(gh api "repos/$repo/actions/runs/${GITHUB_RUN_ID:?}" --jq .workflow_id)"
deadline=$(( $(date +%s) + wait_minutes * 60 ))

while :; do
  # One line per workflow: its newest push or merge-queue run for the commit.
  runs="$(gh api --paginate "repos/$repo/actions/runs?head_sha=$sha&per_page=100" \
    --jq ".workflow_runs[] | select(.workflow_id != $own_workflow) | select(.event == \"push\" or .event == \"merge_group\") | [.workflow_id, .run_number, .run_attempt, .name, .status, (.conclusion // \"\"), .html_url] | @tsv" |
    sort -t$'\t' -k1,1n -k2,2nr -k3,3nr | awk -F'\t' '!seen[$1]++')"
  failed="$(awk -F'\t' '$5 == "completed" && $6 != "success" && $6 != "skipped" && $6 != "neutral" {print $4 " (" $6 "): " $7}' <<<"$runs")"
  running="$(awk -F'\t' '$5 != "completed" {print $4 ": " $7}' <<<"$runs")"
  if [ -n "$failed" ]; then
    echo "::error title=Checks failed::$sha has failing checks, so it isn't released."
    printf '%s\n' "$failed"
    { echo "### Not released: checks failed on ${sha:0:12}"; echo; printf -- '- %s\n' "$failed"; } >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
    exit 1
  fi
  if [ -z "$running" ]; then
    if [ -n "$runs" ]; then
      echo "Every check on $sha passed:"
      awk -F'\t' '{print "  " $4 ": " $6}' <<<"$runs"
    else
      echo "No push checks ran on $sha."
    fi
    exit 0
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "::error title=Checks still running::$sha's checks didn't finish within $wait_minutes minutes."
    printf '%s\n' "$running"
    exit 1
  fi
  echo "Waiting for:"
  printf '  %s\n' "$running"
  sleep 30
done
