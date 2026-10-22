#!/usr/bin/env bash
set -euo pipefail

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

Maintainer checklist:
- Review upstream changes and retain the fork's README, ownership, and automation settings.
- Require CI and CodeQL checks before merging.
- Use **Create a merge commit** to preserve ancestry; do not squash or rebase this PR.

With the default GITHUB_TOKEN, GitHub may require a maintainer to approve PR workflow runs. An optional UPSTREAM_SYNC_TOKEN can enable automatic runs and sync changes to workflow files; see docs/maintaining.md.
BODY
  printf '\nUpstream commit: `%s`\n' "$upstream_sha" >> "$body_file"
  pr_number=$(gh pr list --base main --head "$sync_branch" --state open --json number --jq '.[0].number // empty')
  if [ -n "$pr_number" ]; then
    gh pr edit "$pr_number" --title 'chore: sync upstream main' --body-file "$body_file"
  else
    gh pr create --base main --head "$sync_branch" --title 'chore: sync upstream main' --body-file "$body_file"
  fi
  gh pr view "$sync_branch" --json url --jq '.url' >> "$GITHUB_STEP_SUMMARY"
}

sync_upstream
