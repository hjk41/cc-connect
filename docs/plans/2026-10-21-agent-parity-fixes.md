# Agent parity fixes — 2026-10-21

Source: Codex audit at `/private/tmp/cc-connect-agent-parity-20261021/audit.json`
against `88d0bf52`, plus its controlled probes in `probe-results.jsonl`.

Baseline: Claude Code. Compared agents: Codex, Cursor, Devin, dsh.

## Verdict on the audit

The concrete findings are valid. I re-read every referenced production path and
confirmed the behavior; the engine-level reproductions in `probe-results.jsonl`
match the code.

| Finding | Verdict | Evidence |
|---|---|---|
| P1 card actions/renderers target the global agent | Confirmed | `executeCardAction` `/reasoning`, `/mode`, `/provider` use `e.agent`/`e.sessions`; `renderModeCard`/`renderReasoningCard`/`renderProviderCard` use `e.agent`. Typed commands already use `commandContext`/`sessionContextForKey`. |
| P1 workspace `/model` loses to provider default | Confirmed | `switchModelOnAgent(..., persistConfig=false)` only calls `SetModel`; Codex/Claude `GetModel` and `StartSession` let `providers[active].Model` win. |
| P1 workspace option snapshot incomplete | Confirmed | Codex snapshot drops `env`, `system_prompt`, `append_system_prompt`; ACP snapshot drops `cliExtraArgs` (Devin/dsh wrapper args); Cursor has no `WorkspaceAgentOptionSnapshotter`. |
| P1 `/reasoning` wipes context | Confirmed | `applyReasoningSelection` clears agent session id and history unless the agent implements `LiveReasoningSwitcher` or `ResumeReasoningApplier`; Codex/Claude/Cursor implement neither. Codex app-server actually accepts `effort` on every `turn/start`, so a live switch is possible. |
| P2 Codex native wiring | Confirmed | No `thread/compact/start`, no `turn/interrupt`, `mcpServer/elicitation/request` falls into the `-32601` default. Protocol methods exist in the installed schema. |
| P2 Codex session ownership | Confirmed | `SessionIDValidator` is implemented by Claude/Cursor/ACP but not Codex, so the shared cross-workspace guard is skipped. |
| P2 Codex usage home | Confirmed | `GetUsage` reads `$CODEX_HOME` from the process env only; `codex_home`, config env, session env and provider env are ignored. |

`feature_differences` in the audit are mostly real but are not all bridge bugs:
some are backend capability differences (Cursor headless compaction, quota
APIs, ACP image/question support). Those are listed under "Deferred" rather
than silently claimed as fixed.

One adjacent bug found while validating: `runCompress` and the auto-compress
hook also read `e.agent` instead of the session's workspace agent. It is fixed
with the same workspace-resolution change.

## Fix plan

### Batch 1 — core workspace correctness

1. Resolve card rendering and card actions through `sessionContextForKey`:
   `renderModeCard`, `renderReasoningCard`, `renderProviderCard`,
   `executeCardAction` (`/mode`, `/reasoning`, `/provider`).
2. Pass the workspace compressor into `cmdCompress`/`runCompress` and resolve
   the agent in the auto-compress hook instead of using `e.agent`.
3. Pin explicit model choices for Codex and Claude:
   - add `modelPinned` (same semantics as Cursor);
   - `SetModel` pins, `SetActiveProvider` unpins;
   - `GetModel` and `StartSession` prefer the pinned model over the provider
     default.
4. On workspace-agent creation, after providers are wired, apply the
   persisted workspace model override through `ModelSwitcher.SetModel` so a
   recreated agent keeps the user's choice.

### Batch 2 — workspace option snapshots

1. Codex: add `env`, `system_prompt`, `append_system_prompt` to
   `WorkspaceAgentOptions`.
2. Claude: add `system_prompt`, `append_system_prompt` (env already present).
3. ACP: round-trip `cliExtraArgs` as part of the `cmd` array so Devin/dsh
   wrapper arguments survive cloning.
4. Cursor: implement `WorkspaceAgentOptionSnapshotter` with `cmd`/extra args,
   `env`, `mode`, `model`, `reasoning_effort`.
5. Regression tests: each snapshot round-trips through `New()` and the engine
   clone path.

### Batch 3 — reasoning continuity

1. Codex app-server: implement `SetLiveReasoningEffort` on the session; the
   next `turn/start` already carries `effort`, so no context reset is needed.
2. Codex exec and Claude: implement `ResumeReasoningApplier`; the saved session
   id is kept and the next resume starts with the new effort.
3. Cursor: implement `ResumeReasoningApplier` only if the CLI honors `--model`
   on `--resume`; otherwise keep the current reset and document it.
4. Tests: engine keeps session id/history for resume-appliers; app-server live
   switch does not clear state.

### Batch 4 — Codex P2s

1. `ValidateSessionID`: locate the rollout under the effective `CODEX_HOME`,
   parse its `session_meta` `cwd`, and require it to match the agent work dir.
2. Usage home: resolve the effective Codex home from `codex_home`, config env,
   session env and active provider env (last wins), then read `auth.json`.
3. Native compaction: `CompressCommand()` returns `/compact` for app-server;
   `Send("/compact")` calls `thread/compact/start` and waits for
   `thread/compacted` before emitting the completion event. Exec backend keeps
   the current empty command.
4. Turn interruption: `CancelTurn()` calls `turn/interrupt` with the current
   thread and turn id.
5. MCP elicitation: handle `mcpServer/elicitation/request` instead of
   returning `-32601`. Minimum viable: forward URL/form prompts to the user
   through the existing permission/question channel, return `accept`/`decline`
   with the supplied content. If form fidelity cannot be made correct in this
   pass, leave the handler in place but document the remaining limitation.
6. Tests for all four paths.

### Batch 5 — validation

- `go test ./...`, `go test -race ./...` for touched packages, `go test
  ./core/ -run TestCUJ`, `go vet ./...`, `go build ./...`.
- Build the daemon binary, back up the running one, install, restart the
  `cc-connect` launch agents.
- Feishu CLI (`lark-cli --profile finsearch`, `--as user`) checks against the
  live Codex bot:
  - workspace binding still points at `/Users/hct/cc-connect`;
  - `/model` switch is effective and does not revert;
  - `/reasoning` change keeps the conversation (unique-code recall);
  - `/compact` completes natively and the next turn still has context;
  - `/stop` interrupts a running turn without killing the session;
  - `/usage` reads the configured Codex home;
  - restart preserves the conversation.
- Card-only actions are covered by engine tests because the Feishu CLI cannot
  synthesize card callbacks; typed commands exercise the same renderers.

## Deferred and why

- `/allow` for non-Claude agents: their permission models do not expose a
  dynamic allowlist; mapping it to modes would silently change behavior.
- File-backed custom commands for Codex/Cursor/Devin/dsh: the registry can
  list files, but the backends do not execute those slash commands reliably.
- `/provider` for Devin, `/usage` for Cursor/Devin, Cursor context occupancy:
  the underlying CLIs/ACP sessions do not expose the data.
- Cursor/ACP compaction and general interactive questions: backend protocols
  do not provide a compatible channel.
- MCP elicitation form fidelity may remain limited to the common scalar/enum
  case; full nested JSON forms are a separate UI feature.
