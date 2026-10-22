#!/usr/bin/env bash
set -euo pipefail

# protected_paths lists merge changes the fork owns and that must not land
# without human review. Keep this list in sync with docs/maintaining.md.
protected_paths() {
  git diff --name-only HEAD^1 HEAD |
    grep -E '^(\.github/|README\.md$|README\.zh-CN\.md$|CODEOWNERS$|docs/maintaining\.md$)' || true
}

# actions_gh uses the job's own token for Actions API calls so a
# UPSTREAM_SYNC_TOKEN only needs contents/pull-requests/workflows scopes.
actions_gh() {
  GH_TOKEN="${ACTIONS_TOKEN:-${GH_TOKEN:-}}" gh "$@"
}

lookup_pr_number() {
  local branch=$1 n i
  for i in $(seq 1 10); do
    n=$(gh pr view "$branch" --json number --jq '.number' 2>/dev/null || true)
    if [ -n "$n" ]; then
      printf '%s\n' "$n"
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_ci_and_merge() {
  local branch=$1 pr_number=$2
  local before run_id i run_sha pr_sha protected

  # GITHUB_TOKEN-created PRs do not trigger pull_request workflows. Dispatch
  # CI explicitly on the sync branch, then merge only after the required check
  # reports success. workflow_dispatch is exempt from the no-recursive-runs rule.
  before=$(actions_gh run list --workflow=ci.yml --branch "$branch" --limit 1 --json databaseId --jq '.[0].databaseId // 0' 2>/dev/null || true)
  before=${before:-0}
  actions_gh workflow run ci.yml --ref "$branch"

  run_id=""
  for i in $(seq 1 30); do
    run_id=$(actions_gh run list --workflow=ci.yml --branch "$branch" --limit 1 --json databaseId --jq '.[0].databaseId // 0' 2>/dev/null || true)
    if [ -n "$run_id" ] && [ "$run_id" != "0" ] && [ "$run_id" != "$before" ]; then
      break
    fi
    run_id=""
    sleep 2
  done
  if [ -z "$run_id" ]; then
    echo "CI did not start for \`$branch\`." >> "$GITHUB_STEP_SUMMARY"
    return 1
  fi

  if ! actions_gh run watch "$run_id" --exit-status; then
    {
      echo '## Upstream sync CI failed'
      echo "Run: https://github.com/${GH_REPO}/actions/runs/${run_id}"
      echo 'The sync PR was left open and `main` was not updated.'
    } >> "$GITHUB_STEP_SUMMARY"
    return 1
  fi

  run_sha=$(actions_gh run view "$run_id" --json headSha --jq .headSha)
  pr_sha=$(gh pr view "$pr_number" --json headRefOid --jq .headRefOid)
  if [ "$run_sha" != "$pr_sha" ]; then
    echo "Sync branch moved while CI was running; not merging PR #$pr_number." >> "$GITHUB_STEP_SUMMARY"
    return 1
  fi

  protected=$(protected_paths)
  if [ -n "$protected" ]; then
    {
      echo '## Manual review required'
      echo 'CI passed, but the sync changes fork-owned files:'
      echo '```text'
      echo "$protected"
      echo '```'
      echo 'The PR was left open and was not merged.'
    } >> "$GITHUB_STEP_SUMMARY"
    return 0
  fi

  if ! gh pr merge "$pr_number" --merge; then
    echo "Could not merge PR #$pr_number; it was left open (the base may have moved)." >> "$GITHUB_STEP_SUMMARY"
    return 1
  fi
  echo "Merged sync PR #$pr_number after CI passed." >> "$GITHUB_STEP_SUMMARY"
}

sync_upstream() {
  : "${GITHUB_STEP_SUMMARY:?}" "${RUNNER_TEMP:?}" "${GH_REPO:?}"
  local sync_branch=${SYNC_BRANCH:-automation/upstream-sync}
  local upstream_url=${UPSTREAM_URL:-https://github.com/chenhg5/cc-connect.git}
  local expected upstream_sha body_file pr_number
  local needs_merge=true

  git fetch --no-tags "$upstream_url" main:refs/remotes/upstream/main
  if git merge-base --is-ancestor upstream/main HEAD; then
    echo 'Upstream main is already included in this fork.' >> "$GITHUB_STEP_SUMMARY"
    return
  fi

  expected=$(git ls-remote --heads origin "refs/heads/$sync_branch" | cut -f1)
  upstream_sha=$(git rev-parse upstream/main)
  if [ -n "$expected" ]; then
    git fetch --no-tags origin "$expected"
    if git merge-base --is-ancestor HEAD "$expected" && git merge-base --is-ancestor upstream/main "$expected"; then
      git checkout --detach "$expected"
      needs_merge=false
    fi
  fi

  git config user.name 'github-actions[bot]'
  git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
  if [ "$needs_merge" = true ] && ! git merge --no-ff --no-edit upstream/main; then
    {
      echo '## Upstream merge needs conflict resolution'
      echo 'No branch was pushed. Resolve these paths locally and open a sync PR:'
      echo '```text'
      git diff --name-only --diff-filter=U
      echo '```'
    } >> "$GITHUB_STEP_SUMMARY"
    git merge --abort
    return 1
  fi

  git push --force-with-lease="refs/heads/$sync_branch:$expected" origin "HEAD:refs/heads/$sync_branch"
  body_file="$RUNNER_TEMP/upstream-sync-body.md"
  cat > "$body_file" <<'BODY'
Merge the latest `chenhg5/cc-connect:main` into this maintained fork, preserving upstream commit history.

Automation:
- Dispatches CI on this branch and waits for the required `CI passed` check.
- Merges with a merge commit only when CI passes, so `main` stays green.
- Leaves this PR open when CI fails or the merge changes fork-owned files (`.github/`, READMEs, CODEOWNERS, `docs/maintaining.md`).

Do not squash or rebase this PR; that would lose upstream ancestry. See docs/maintaining.md.
BODY
  printf '\nUpstream commit: `%s`\n' "$upstream_sha" >> "$body_file"
  pr_number=$(gh pr list --base main --head "$sync_branch" --state open --json number --jq '.[0].number // empty')
  if [ -n "$pr_number" ]; then
    gh pr edit "$pr_number" --title 'chore: sync upstream main' --body-file "$body_file"
  else
    gh pr create --base main --head "$sync_branch" --title 'chore: sync upstream main' --body-file "$body_file"
    pr_number=$(lookup_pr_number "$sync_branch" || true)
  fi
  if [ -z "$pr_number" ]; then
    echo 'Could not determine the sync PR number.' >> "$GITHUB_STEP_SUMMARY"
    return 1
  fi
  gh pr view "$sync_branch" --json url --jq '.url' >> "$GITHUB_STEP_SUMMARY"

  wait_for_ci_and_merge "$sync_branch" "$pr_number"
}

sync_upstream
