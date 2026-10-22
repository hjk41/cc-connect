# DSH daily upstream sync

The daily upstream sync runs on the maintainer machine through cc-connect's
scheduler and the DSH agent. It is not a GitHub Actions schedule.

- Project and data: `dsh-bot` in `~/.cc-connect/dsh-bot-data`
- Workspace: the chat bound to the `cc-connect` checkout
- Schedule: `0 1 * * *` (01:00 Asia/Tokyo)
- Session mode: `new_per_run`
- Permission mode: `bypassPermissions` (the run is unattended)
- Timeout: 60 minutes

The GitHub `Upstream Sync` workflow is a manual fallback. It only prepares a
pull request; it never merges.

## Setup

Create the job with the chat's session key (the chat must be bound to the
repository workspace). The CLI does not expose the permission mode, so set it
with a second step:

```bash
cc-connect cron add \
  --data-dir ~/.cc-connect/dsh-bot-data \
  --project dsh-bot \
  --session-key 'feishu:<chat-id>:<user-id>' \
  --cron '0 1 * * *' \
  --session-mode new_per_run \
  --timeout-mins 60 \
  --desc 'Daily upstream sync (DSH)' \
  --prompt "$(cat docs/upstream-sync-agent-prompt.txt)"

cc-connect cron edit <job-id> mode bypassPermissions
```

The equivalent API call is `POST /cron/add` on the project's management
socket, with `"project": "dsh-bot"`, the session key, `"cron_expr": "0 1 * * *"`,
the prompt, `"session_mode": "new_per_run"`, `"timeout_mins": 60`, and
`"mode": "bypassPermissions"`.

## Prompt

The scheduler runs this prompt in a fresh DSH session. Keep the copy in
[upstream-sync-agent-prompt.txt](upstream-sync-agent-prompt.txt) in sync with
the job's stored prompt.

```text
You are the daily upstream sync agent for hjk41/cc-connect.

Work in /Users/hct/cc-connect. Merge chenhg5/cc-connect:main into this fork,
resolve conflicts, and keep CI green. Do not stop at the first conflict;
resolve it by reading both sides.

Steps:
1. cd /Users/hct/cc-connect. Run git status. If it is not clean, report and stop.
2. git fetch upstream --prune. If upstream/main is already an ancestor of HEAD,
   report "already up to date" and stop.
3. Create a fresh sync branch from origin/main (delete an old local
   automation/dsh-upstream-sync first). Merge upstream/main with --no-ff.
4. Resolve every conflict. Preserve fork features (Cursor, Devin, dsh, README,
   CI) while taking upstream's changes. Do not blindly use --ours/--theirs.
5. Verify locally: gofmt -l on changed Go files; go build ./...; go vet on
   changed packages; go test ./... (or affected packages plus ./core if the
   full suite is impractical); bash .github/tests/upstream-sync.sh; actionlint
   if available.
6. Commit the merge, push the branch, and open or update a PR against main.
   Always pass -R hjk41/cc-connect to gh commands; the default remote points at
   upstream.
7. Wait for required checks (gh pr checks <num> --required --watch --fail-fast).
   On failure, read the logs, fix the root cause, push again, and repeat until
   green. Do not skip or delete tests and do not weaken lint.
8. Merge with a merge commit (gh pr merge <num> --merge). Never squash or
   rebase. Do not use --admin to bypass checks. Confirm the push-to-main CI is
   green.
9. Report the upstream SHA, merge SHA, PR and CI links, a conflict summary, and
   the commands run. If CI cannot be made green or a conflict cannot be
   resolved safely within 60 minutes, stop, leave main unchanged, and report
   exactly what is blocking.

Safety: never force-push main; never discard uncommitted user work. Keep
changes under .github/, the READMEs, CODEOWNERS, and docs/maintaining.md
conservative.
```
