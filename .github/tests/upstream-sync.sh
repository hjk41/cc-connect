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
export SYNC_TEST_OPEN_PR=''

mkdir -p "$sync_test_dir/bin"
cat > "$sync_test_dir/bin/gh" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$SYNC_TEST_GH_LOG"
case "$1 $2" in
  'pr list') if [ -n "$SYNC_TEST_OPEN_PR" ]; then echo 7; fi ;;
  'pr create' | 'pr edit') ;;
  'pr view') echo 'https://example.invalid/test/fork/pull/7' ;;
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
echo 'PASS: sync preserves merge parents, opens a PR, and leaves main unchanged'

git -C "$sync_test_dir/work" checkout --quiet main
git -C "$sync_test_dir/work" reset --quiet --hard "$base_sha"
export SYNC_TEST_OPEN_PR=7
run_sync > "$sync_test_dir/output" 2>&1
test "$(git --git-dir="$sync_test_dir/origin.git" rev-parse refs/heads/automation/upstream-sync)" = "$sync_sha"
grep -q '^pr edit 7 ' "$SYNC_TEST_GH_LOG"
echo 'PASS: repeated sync reuses the existing merge and PR'

printf 'upstream edit\n' > "$sync_test_dir/author/base.txt"
git -C "$sync_test_dir/author" commit --quiet -am upstream-conflict
git -C "$sync_test_dir/author" push --quiet "$UPSTREAM_URL" main
git -C "$sync_test_dir/work" checkout --quiet main
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
