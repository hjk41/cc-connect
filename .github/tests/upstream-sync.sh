#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../maintenance" && pwd)
sync_test_dir=$(mktemp -d)
trap 'rm -rf "$sync_test_dir"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=sync-test GIT_COMMITTER_NAME=sync-test
export GIT_AUTHOR_EMAIL=sync-test@example.invalid GIT_COMMITTER_EMAIL=sync-test@example.invalid
export RUNNER_TEMP="$sync_test_dir" GH_REPO=test/fork
export GITHUB_STEP_SUMMARY="$sync_test_dir/summary"
export UPSTREAM_URL="$sync_test_dir/upstream.git"
export SYNC_TEST_GH_LOG="$sync_test_dir/gh.log"
export SYNC_TEST_CI_COUNTER="$sync_test_dir/ci-counter"
export SYNC_TEST_HEAD_SHA=deadbeef
export SYNC_TEST_OPEN_PR=''
export SYNC_TEST_CI_FAIL=''

mkdir -p "$sync_test_dir/bin"
cat > "$sync_test_dir/bin/gh" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$SYNC_TEST_GH_LOG"
if [ "$1" = api ]; then exit 0; fi
case "$1 $2" in
  'pr list') if [ -n "$SYNC_TEST_OPEN_PR" ]; then echo 7; fi ;;
  'pr create' | 'pr edit' | 'pr merge') ;;
  'pr view')
    case " $* " in
      *" --json number "*) echo 7 ;;
      *" --json url "*) echo 'https://example.invalid/test/fork/pull/7' ;;
      *" --json headRefOid "*) printf '%s\n' "${SYNC_TEST_HEAD_SHA:-deadbeef}" ;;
      *) exit 2 ;;
    esac ;;
  'workflow run')
    n=$(cat "$SYNC_TEST_CI_COUNTER" 2>/dev/null || echo 0)
    echo $((n + 1)) > "$SYNC_TEST_CI_COUNTER" ;;
  'run list')
    cat "$SYNC_TEST_CI_COUNTER" 2>/dev/null || echo 0 ;;
  'run watch')
    if [ "${SYNC_TEST_CI_FAIL:-}" = 1 ]; then exit 1; fi ;;
  'run view')
    printf '%s\n' "${SYNC_TEST_HEAD_SHA:-deadbeef}" ;;
  *) exit 2 ;;
esac
MOCK
# GitHub runners need only standard shell tools for this test. Keep an
# installed ripgrep from hiding accidental dependencies on developer tools.
cat > "$sync_test_dir/bin/rg" <<'MOCK'
#!/usr/bin/env bash
echo 'FAIL: upstream sync tests must not require ripgrep' >&2
exit 127
MOCK
chmod +x "$sync_test_dir/bin/gh" "$sync_test_dir/bin/rg"
export PATH="$sync_test_dir/bin:$PATH"

git init --quiet --initial-branch=main "$sync_test_dir/author"
printf 'base\n' > "$sync_test_dir/author/base.txt"
git -C "$sync_test_dir/author" add base.txt
git -C "$sync_test_dir/author" commit --quiet -m base
git clone --quiet --bare "$sync_test_dir/author" "$sync_test_dir/origin.git"
git clone --quiet --bare "$sync_test_dir/author" "$UPSTREAM_URL"
git clone --quiet "$sync_test_dir/origin.git" "$sync_test_dir/work"
base_sha=$(git -C "$sync_test_dir/work" rev-parse HEAD)

run_sync() {
  (cd "$sync_test_dir/work" && bash "$script_dir/upstream-sync.sh")
}

reset_work_main() {
  git -C "$sync_test_dir/work" checkout --quiet main
  git -C "$sync_test_dir/work" reset --quiet --hard "$base_sha"
}

run_sync > "$sync_test_dir/output" 2>&1
test ! -e "$SYNC_TEST_GH_LOG"
if git --git-dir="$sync_test_dir/origin.git" show-ref --verify --quiet refs/heads/automation/upstream-sync; then
  echo 'FAIL: up-to-date sync created a branch' >&2
  exit 1
fi
echo 'PASS: up-to-date upstream creates no branch or PR'

printf 'new upstream file\n' > "$sync_test_dir/author/new.txt"
git -C "$sync_test_dir/author" add new.txt
git -C "$sync_test_dir/author" commit --quiet -m upstream-change
git -C "$sync_test_dir/author" push --quiet "$UPSTREAM_URL" main
upstream_sha=$(git -C "$sync_test_dir/author" rev-parse HEAD)
run_sync > "$sync_test_dir/output" 2>&1
sync_sha=$(git --git-dir="$sync_test_dir/origin.git" rev-parse refs/heads/automation/upstream-sync)
test "$(git --git-dir="$sync_test_dir/origin.git" rev-parse main)" = "$base_sha"
test "$(git -C "$sync_test_dir/work" rev-parse HEAD^1)" = "$base_sha"
test "$(git -C "$sync_test_dir/work" rev-parse HEAD^2)" = "$upstream_sha"
grep -q '^pr create ' "$SYNC_TEST_GH_LOG"
grep -q '^workflow run ci.yml --ref automation/upstream-sync$' "$SYNC_TEST_GH_LOG"
grep -q '^run watch 1 --exit-status$' "$SYNC_TEST_GH_LOG"
grep -q '^api repos/test/fork/statuses/deadbeef ' "$SYNC_TEST_GH_LOG"
grep -q '^pr merge 7 --merge$' "$SYNC_TEST_GH_LOG"
grep -q 'Merged sync PR #7 after CI passed' "$GITHUB_STEP_SUMMARY"
echo 'PASS: sync preserves merge parents, dispatches CI, and merges only after CI passes'

reset_work_main
export SYNC_TEST_OPEN_PR=7
run_sync > "$sync_test_dir/output" 2>&1
test "$(git --git-dir="$sync_test_dir/origin.git" rev-parse refs/heads/automation/upstream-sync)" = "$sync_sha"
grep -q '^pr edit 7 ' "$SYNC_TEST_GH_LOG"
grep -q '^run watch 2 --exit-status$' "$SYNC_TEST_GH_LOG"
test "$(grep -c '^pr merge 7 --merge$' "$SYNC_TEST_GH_LOG")" = 2
echo 'PASS: repeated sync reuses the existing merge and PR'

printf 'fork-owned readme\n' > "$sync_test_dir/author/README.md"
git -C "$sync_test_dir/author" add README.md
git -C "$sync_test_dir/author" commit --quiet -m upstream-readme
git -C "$sync_test_dir/author" push --quiet "$UPSTREAM_URL" main
reset_work_main
: > "$SYNC_TEST_GH_LOG"
run_sync > "$sync_test_dir/output" 2>&1
grep -q '^run watch 3 --exit-status$' "$SYNC_TEST_GH_LOG"
grep -q '^api repos/test/fork/statuses/deadbeef ' "$SYNC_TEST_GH_LOG"
if grep -q '^pr merge ' "$SYNC_TEST_GH_LOG"; then
  echo 'FAIL: protected sync unexpectedly merged' >&2
  exit 1
fi
grep -q 'Manual review required' "$GITHUB_STEP_SUMMARY"
grep -q '^README.md$' "$GITHUB_STEP_SUMMARY"
echo 'PASS: fork-owned files require review even when CI passes'

printf 'ci failure\n' > "$sync_test_dir/author/ci-fail.txt"
git -C "$sync_test_dir/author" add ci-fail.txt
git -C "$sync_test_dir/author" commit --quiet -m upstream-ci-fail
git -C "$sync_test_dir/author" push --quiet "$UPSTREAM_URL" main
reset_work_main
: > "$SYNC_TEST_GH_LOG"
export SYNC_TEST_CI_FAIL=1
if run_sync > "$sync_test_dir/output" 2>&1; then
  echo 'FAIL: sync unexpectedly succeeded when CI failed' >&2
  exit 1
fi
export SYNC_TEST_CI_FAIL=''
grep -q '^run watch 4 --exit-status$' "$SYNC_TEST_GH_LOG"
if grep -q '^api repos/test/fork/statuses/' "$SYNC_TEST_GH_LOG"; then
  echo 'FAIL: failed CI posted a success status' >&2
  exit 1
fi
if grep -q '^pr merge ' "$SYNC_TEST_GH_LOG"; then
  echo 'FAIL: sync merged even though CI failed' >&2
  exit 1
fi
grep -q 'Upstream sync CI failed' "$GITHUB_STEP_SUMMARY"
echo 'PASS: failed CI leaves the PR open and fails the sync run'

sync_sha=$(git --git-dir="$sync_test_dir/origin.git" rev-parse refs/heads/automation/upstream-sync)
printf 'upstream edit\n' > "$sync_test_dir/author/base.txt"
git -C "$sync_test_dir/author" commit --quiet -am upstream-conflict
git -C "$sync_test_dir/author" push --quiet "$UPSTREAM_URL" main
reset_work_main
printf 'fork edit\n' > "$sync_test_dir/work/base.txt"
git -C "$sync_test_dir/work" commit --quiet -am fork-conflict
git -C "$sync_test_dir/work" push --quiet origin main
fork_sha=$(git -C "$sync_test_dir/work" rev-parse HEAD)
if run_sync > "$sync_test_dir/output" 2>&1; then
  echo 'FAIL: conflicting sync unexpectedly succeeded' >&2
  exit 1
fi
test "$(git --git-dir="$sync_test_dir/origin.git" rev-parse main)" = "$fork_sha"
test "$(git --git-dir="$sync_test_dir/origin.git" rev-parse refs/heads/automation/upstream-sync)" = "$sync_sha"
test -z "$(git -C "$sync_test_dir/work" status --porcelain)"
grep -q '^base.txt$' "$GITHUB_STEP_SUMMARY"
echo 'PASS: conflicts are reported and aborted without pushing either branch'
