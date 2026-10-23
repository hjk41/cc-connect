# Codex next-turn live mode switching

Status: implemented, deployed, Feishu-verified (2026-10-23)
Date: 2026-10-23
Owner: Codex implementation, dsh review/acceptance

## Context

`/mode` on the Codex app-server backend currently closes the agent process and
resumes the same thread. History is preserved, but every switch costs a process
restart and re-handshake. Codex app-server supports per-turn overrides on
`turn/start`:

- `approvalPolicy`
- `sandboxPolicy`
- `approvalsReviewer`

The mode overhaul (commit `eccce12f`) added the five canonical modes and sends
explicit permission settings on thread start/resume and every turn. This change
adds `LiveModeSwitcher` so `/mode` can apply from the next turn without killing
the process.

## Scope

Implement live switching for:

- same sandbox tier transitions (e.g. `workspace` <-> `auto-review` <-> `full-auto`)
- transitions to `read-only` (e.g. `workspace` -> `suggest`)
- transitions to `danger-full-access` (e.g. `workspace` -> `yolo`)
- transitions to `workspace-write` **when this session has previously observed
  a workspace sandbox policy** (remembered from thread start/resume or a prior
  live switch)

Fallback: if the target is `workspace-write` and no workspace policy has ever
been observed in this session, return `false` so the engine keeps the existing
process-restart + thread-resume path. This avoids guessing `networkAccess` or
writable roots.

Do not implement named/custom permission profiles.

## Semantics

`SetLiveMode(mode) bool`:

- Validate the mode as an app-server mode via the existing resolver.
- Return false if the session is not alive, has no thread, or has not yet
  observed an effective sandbox tier.
- Same sandbox tier: store the new canonical mode, clear any pending per-turn
  sandbox policy, return true.
- Target `read-only`: store the mode, set a per-turn
  `{"type":"readOnly","networkAccess":false}` policy, return true.
- Target `danger-full-access`: store the mode, set a per-turn
  `{"type":"dangerFullAccess"}` policy, return true.
- Target `workspace-write` with a remembered workspace policy: store the mode,
  set that policy for the next turn, return true.
- Target `workspace-write` without a remembered policy: return false and do not
  mutate the mode.

The switch applies to the next `turn/start`; an in-flight turn is unaffected.
The engine already clears `approveAll` before calling `SetLiveMode`, so a
session-wide auto-approval does not survive a mode change.

## State

In `appServerSession`, guard mode and sandbox state with `runtimeMu` (or a
dedicated mutex):

```go
mode                 string
effectiveSandboxTier string
lastWorkspacePolicy  map[string]any
turnSandboxPolicy    map[string]any
```

On `thread/start` and `thread/resume`:

- Decode the effective sandbox object from the response.
- Derive the tier (`read-only`, `workspace-write`, `danger-full-access`).
- Store `effectiveSandboxTier`.
- If the tier is `workspace-write`, store a deep copy of the effective sandbox
  object as `lastWorkspacePolicy` (including `writableRoots`, `networkAccess`,
  `excludeTmpdirEnvVar`, `excludeSlashTmp`).
- Clear any `turnSandboxPolicy` because the thread now carries the effective
  sandbox.

`SetLiveMode` updates `mode`, `effectiveSandboxTier`, `lastWorkspacePolicy`
(copy), and `turnSandboxPolicy` atomically.

`Send` reads a snapshot of `mode` + `turnSandboxPolicy`, then builds
`turn/start` with:

- `approvalPolicy`
- `approvalsReviewer`
- `sandboxPolicy` only when `turnSandboxPolicy` is non-nil

Do not send `sandboxPolicy` for ordinary same-tier turns. Do not send `sandbox`
on `turn/start`.

Keep sending `sandboxPolicy` on subsequent turns while it is set, or clear it
only after a successful `turn/start`; either is acceptable if the thread state
remains correct. Prefer keeping the explicit policy until the thread is
restarted or another live switch replaces it.

## UI wording

`LiveModeSwitcher` now means "session updated; applies from the next turn" for
Codex. Update `MsgModeChangedLive` in all five languages and the corresponding
engine test so the message does not claim the current turn changed immediately.

Suggested EN: `Permission mode switched to **%s**. The current session will use it from the next turn.`
Suggested ZH: `权限模式已切换为 **%s**，当前会话将从下一轮开始使用。`

## Engine behavior

- `applyLiveModeChange` remains the same; it calls `SetLiveMode`.
- If `SetLiveMode` returns true, the engine must not clean up the interactive
  state or restart the process. `approveAll` is already cleared by
  `applyModeSelection`.
- If false, the existing restart/resume path remains the fallback.
- Add a regression test proving a live switch does not call
  `cleanupInteractiveState` and preserves the running session object.

## Tests

Adapter:
- same-tier switch returns true, updates mode, next turn carries new
  `approvalPolicy`/`approvalsReviewer`, no `sandboxPolicy`
- workspace -> read-only switch returns true, next turn carries
  `{"type":"readOnly"}`, no process restart
- workspace -> danger-full-access switch returns true, next turn carries
  `{"type":"dangerFullAccess"}`
- read-only -> workspace after a remembered workspace policy returns true and
  restores `networkAccess`, writable roots and tmp flags
- read-only -> workspace without a remembered policy returns false and does not
  mutate mode
- not alive / no thread / no observed sandbox returns false
- mode + policy state is race-safe under `-race`
- existing thread/turn payload tests remain green (ordinary turns still omit
  `sandboxPolicy`)

Core:
- live switch clears `approveAll` (already covered; keep)
- live switch does not clean up interactive state
- `MsgModeChangedLive` wording test updated for all languages

Real binary:
- controlled app-server smoke: same-tier, read-only and danger-full-access
  per-turn overrides; verify effective reviewer/sandbox after the next turn
- ensure `networkAccess` is preserved when restoring workspace policy

## Rollout and Feishu verification

1. Implement and run `gofmt`, `go test ./agent/codex/`, `go test -race
   ./agent/codex/`, `go build ./...`, `go vet`, `go test ./core/ -run TestCUJ`.
2. Build and install a new `v1.5.7-codex-modes.2` binary.
3. Restart `com.cc-connect.codex-bot`.
4. Feishu verification:
   - `workspace` -> `auto-review`: no new `codex app-server session started`
     log, next turn uses `auto_review`; history intact.
   - `auto-review` -> `suggest`: no new session process, next turn uses
     `readOnly` + `user`; a file write still produces the permission card.
   - `suggest` -> `workspace`: if the session previously observed workspace,
     switch live using the remembered policy; no new process; workspace write
     runs without a card.
   - `workspace` -> `full-auto`: live same-tier switch, no new process.
   - `suggest` -> `workspace` after a fresh session that never observed
     workspace: fallback restart/resume is acceptable; verify history is kept.
5. Confirm `/mode` card and PID stability across supported switches.

## Verification (2026-10-23)

Code checks:
- `go test ./agent/codex/` and `go test -race ./agent/codex/` pass.
- `go test ./core/` and `go test ./core/ -run TestCUJ` pass.
- `go build ./...` and `go vet ./agent/codex/ ./core/` pass.
- Real Codex 0.163.0 smoke: six switches preserved PID/thread identity and
  verified reviewer, sandbox, and restored workspace network access.

Feishu E2E through `SearchTech`, one app-server process (PID 67415) for the
whole sequence:
- `auto-review` -> `workspace`: live switch, no new process; next turn
  returned `WS_LIVE`.
- `workspace` -> `suggest`: live switch, no new process; workspace write
  produced a Bash permission card, proving the per-turn `readOnly` policy was
  applied; after approval the write completed.
- `suggest` -> `workspace`: live switch using the remembered workspace policy,
  no new process; workspace write ran without a card.
- `workspace` -> `full-auto`: live same-tier switch, no new process.
- `workspace` -> `yolo`: live switch to `dangerFullAccess`, no new process.
- `yolo` -> `auto-review`: live switch restoring the remembered workspace
  policy, no new process; final mode `auto-review`.
- `MsgModeChangedLive` now says the session uses the mode from the next turn.

Deployment:
- Binary `v1.5.7-codex-modes.2` installed; `com.cc-connect.codex-bot`
  restarted. `com.cc-connect.service` was not restarted to avoid interrupting
  other active work; it will pick up the binary on its next restart.

## Non-goals

- Named/custom permission profiles
- Hybrid auto-review-shell/manual-MCP preset
- Changing `full-auto` semantics
- Live switching to workspace-write when no workspace policy has ever been
  observed (fallback restart is intentional)
