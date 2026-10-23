# Codex permission modes: workspace + auto-review

Status: implemented, deployed, Feishu-verified (2026-10-23)
Date: 2026-10-23
Owner: Codex implementation, dsh review/acceptance

## Context

CC-Connect's Codex `app_server` adapter currently exposes four modes:

| mode | approvalPolicy | sandbox |
|---|---|---|
| `suggest` | `on-request` | `read-only` |
| `auto-edit` | `never` | `workspace-write` |
| `full-auto` | `never` | `workspace-write` |
| `yolo` | `never` | `danger-full-access` |

This is a lossy projection of Codex's permission model. In particular it has no
equivalent of Codex's **Workspace** preset (`workspace-write` + `on-request` +
`user`) or **Approve for Me** (`workspace-write` + `on-request` +
`auto_review`). `full-auto` is an upstream-original legacy preset that means
"workspace-write, never ask, hard-deny anything requiring approval", not
"auto-approve everything".

The local deployment also has an out-of-repo wrapper at
`~/.cc-connect/bin/codex` that forces `-c 'approvals_reviewer="user"'`. This
overrides the global `approvals_reviewer="auto_review"` and must be removed
after the adapter sends an explicit reviewer on every app-server request.

## Decisions

1. Keep `full-auto` unchanged for backward compatibility.
2. Add `workspace` and `auto-review` (canonical key `auto-review`).
3. Do **not** implement live mode switching in this change. Keep the current
   process-restart + thread-resume behavior; live switching is a follow-up.
4. Remove the local Codex wrapper after the adapter is verified to send an
   explicit `approvalsReviewer` on every thread/turn request.

## Canonical modes

`app_server` backend:

| key | label EN / ZH | sandbox | approvalPolicy | reviewer |
|---|---|---|---|---|
| `suggest` | Read Only / 只读 | `read-only` | `on-request` | `user` |
| `workspace` | Workspace / 工作区 | `workspace-write` | `on-request` | `user` |
| `auto-review` | Approve for Me / 自动审批 | `workspace-write` | `on-request` | `auto_review` |
| `full-auto` | Workspace, No Prompts (Legacy) / 工作区免审批（兼容） | `workspace-write` | `never` | `user` |
| `yolo` | Full Access / 完全访问 | `danger-full-access` | `never` | `user` |

`exec` backend retains `suggest`, `full-auto`, `yolo`; `workspace` and
`auto-review` are rejected with an actionable error because `codex exec` has no
approval IPC.

## Aliases

Preserve all existing aliases and add the new ones:

- `suggest`, `read-only`, `readonly`, `read_only` -> `suggest`
- `workspace`, `workspace-write`, `workspace_write` -> `workspace`
- `auto-review`, `auto_review`, `autoreview`, `approve-for-me`,
  `approve_for_me` -> `auto-review`
- `full-auto`, `fullauto`, `full_auto`, `auto`, `auto-edit`, `autoedit`,
  `auto_edit`, `edit` -> `full-auto`
- `yolo`, `bypass`, `dangerously-bypass` -> `yolo`

Critical: `auto` must continue to mean legacy `full-auto`. Do not reassign it to
`workspace`, or existing configs silently change permissions.

## Internal model

Replace the two-string `appServerModeSettings` with a typed resolver, e.g.:

```go
type codexModeSpec struct {
    Key            string
    Sandbox        string // read-only | workspace-write | danger-full-access
    ApprovalPolicy string // on-request | never
    Reviewer       string // user | auto_review
    AppServerOnly  bool
    Legacy         bool
}
```

One resolver drives:
- `PermissionModes()`
- mode validation
- `thread/start`
- `thread/resume`
- `turn/start`
- exec backend argument construction

Do not scatter a second mode table across validation, UI and request building.

## App-server wiring

`thread/start` and `thread/resume`:
- `approvalPolicy` = spec.ApprovalPolicy
- `sandbox` = spec.Sandbox
- `approvalsReviewer` = spec.Reviewer

`turn/start` (per-turn override):
- `approvalPolicy` = spec.ApprovalPolicy
- `approvalsReviewer` = spec.Reviewer
- Do **not** send `sandboxPolicy` in this version. The thread is started or
  resumed with the correct `sandbox` tier, so the sandbox is already in effect.
  A per-turn `workspaceWrite` policy whose `networkAccess` defaults to `false`
  would override the user's `sandbox_workspace_write.network_access = true` and
  silently disable network access. `sandboxPolicy` belongs to the live-switch
  follow-up, where it must preserve writable roots, network access, and
  temporary-directory settings explicitly.

Do not send `sandbox` on `turn/start` either; the schema uses `sandboxPolicy`
for per-turn overrides, which is deferred. Do not combine a named `permissions`
profile with `sandbox`/`sandboxPolicy` in this change; named profiles are a later
feature.

Decode `approvalPolicy`, `approvalsReviewer` and sandbox/profile information
from `thread/start`/`thread/resume` responses. Log the effective reviewer. If
the server rejects or silently changes the requested settings, surface an error
or warning instead of claiming success.

## Validation and UI

Add an optional core interface:

```go
type ModeValidator interface {
    ValidateMode(mode string) error
}
```

- `cmdMode`: validate before mutating; on error reply with a localized
  "unknown/unsupported mode" message and do not change state.
- Card `/mode` action: validate before `applyModeSelection`.
- `management.go` mode update: validate and return a 4xx error on failure.
- `applyModeSelection` may validate defensively.
- Codex implements `ValidateMode` using the same resolver; unknown explicit
  values are rejected instead of silently falling back to `suggest`.
- Empty/omitted config still defaults to `suggest`.

Add `MsgModeInvalid` to `core/i18n.go` for EN, ZH, ZH-TW, JA, ES.

Correct the misleading `suggest` description: `on-request` does not mean every
tool call is asked; already-permitted actions run. Also correct
`config.example.toml` language that calls `full-auto` "auto-approve everything".

## Exec backend

- `suggest`: read-only + `approval_policy=never` (unchanged).
- `full-auto` / `auto-edit`: workspace-write + `never` (unchanged).
- `yolo`: bypass (unchanged).
- `workspace` / `auto-review`: reject before starting a session.

## Wrapper removal

After the adapter is verified:

1. Back up `~/.cc-connect/bin/codex` to a timestamped non-`codex` name.
2. Remove/rename the active `codex` wrapper so `exec.LookPath("codex")` resolves
   to `/Users/hct/.local/bin/codex`.
3. Confirm the daemon PATH still resolves the real binary.
4. Restart the Codex-backed services so new sessions use the new binary and the
   wrapper is no longer in effect.

## Tests

Codex adapter:
- canonical resolver and alias table, including `auto` -> `full-auto`
- `PermissionModes()` for app_server vs exec
- exact `thread/start`, `thread/resume`, `turn/start` payloads for every mode
- `thread/start`/`thread/resume` carry `sandbox`; `turn/start` carries
  `approvalPolicy` + `approvalsReviewer` and does **not** carry `sandboxPolicy`
  (this version must not tighten network access)
- exec backend rejects `workspace` and `auto-review`
- unknown explicit mode rejected
- response decoding/logging of effective reviewer

Core:
- `cmdMode` invalid mode does not mutate state and returns localized error
- card `/mode` invalid mode does not mutate state
- management API invalid mode returns an error
- mode change still preserves history through resume (existing CUJ)
- session `approveAll` does not survive a mode change (regression; especially
  important for the future live-switch path)

Protocol/real-binary:
- explicit `approvalsReviewer` overrides the global config and any wrapper
  default
- controlled smoke test with the real Codex binary

## Rollout

1. Keep the existing uncommitted Plan D (MCP approval cards) separate.
2. Implement this plan; run `gofmt`, `go test ./agent/codex/`, `go build ./...`,
   `go vet ./...`, `go test ./core/ -run TestCUJ`, and relevant race tests.
3. Build and install the binary.
4. Canary with the wrapper still installed, verifying manual + automatic paths.
5. Remove the wrapper, restart services, re-verify.
6. Run real Feishu verification:
   - `/mode` lists all five modes.
   - `suggest`: outside-sandbox write produces a permission card; MCP tool
     approval produces a card titled `MCP <server>/<tool>`; allow/deny works.
   - `workspace`: workspace-internal write runs; outside-workspace/approval
     request still reaches Feishu.
   - `auto-review`: eligible MCP write is handled by Codex auto-review; no
     `mcp_tool_call` card; result may be approved or denied.
   - `full-auto`: legacy behavior unchanged (no prompts, hard-deny approvals).
   - `yolo`: full access unchanged.
   - aliases: `auto-edit`/`auto` -> full-auto; `approve-for-me` -> auto-review.
7. Report logs/evidence and any residual limitations.

## Verification (2026-10-23)

Code/value checks:
- `go test ./agent/codex/` pass; `go test -race ./agent/codex/` pass.
- `go build ./...` pass; `go vet ./agent/codex/ ./core/` pass.
- `go test ./core/ -run TestCUJ` pass; targeted mode-validation tests pass.
- Real Codex 0.163.0 smoke test: all five modes, explicit reviewer
  overriding a conflicting CLI default, thread/turn resume, and network
  access preserved.

Feishu E2E through the `SearchTech` group:
- `/mode` card lists exactly `suggest`, `workspace`, `auto-review`,
  `full-auto`, `yolo`; current marker correct.
- `workspace`: workspace-internal shell write ran without a permission card.
- `suggest`: outside-sandbox shell write produced a Bash Allow/Deny card;
  replying "允许" resolved it and the write completed.
- `suggest`: `ma3_feedback` MCP approval produced a card titled
  `MCP ma3/ma3_feedback`; approving it let the tool run and return its expected
  not-found error for the fake record id.
- `auto-review`: outside-workspace write was auto-approved with no card;
  `ma3_feedback` MCP call ran with no `mcp_tool_call` card (the tool returned
  its expected not-found error for the deliberately fake record id).
- `full-auto` (legacy): the same `ma3_feedback` call still failed with
  `MCP tool call requires approval, but approval policy is never`; no card.
- Aliases: `approve-for-me` -> `auto-review`; `auto` -> `full-auto`.
- Unknown mode was rejected locally and did not change the active mode.

Deployment:
- Binary `v1.5.7-codex-modes.1` installed; `com.cc-connect.codex-bot` and
  `com.cc-connect.service` restarted.
- Local `~/.cc-connect/bin/codex` wrapper retired (timestamped backup kept);
  `codex` now resolves to `/Users/hct/.local/bin/codex`.
- Logs show `approvalsReviewer=user` for `suggest`/`workspace`/`full-auto`/
  `yolo` and `approvalsReviewer=auto_review` for `auto-review`.

## Non-goals

- Live mode switching (`SetLiveMode`) — next version.
- Named/custom permission profiles.
- A hybrid "auto-review shell, manual MCP" preset.
- Redefining `full-auto`.
