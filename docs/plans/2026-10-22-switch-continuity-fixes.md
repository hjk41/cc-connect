# Runtime switch continuity fixes — 2026-10-22

Audit base: `69d67e88`. Two independent reviews (DSH and Astra/gpt-6-astra)
were merged below. Astra's key correction: **slash `/mode` already keeps the
saved conversation ID**; the destructive fallback is in the mode **card**
path. Several other switches have the same class of bug.

## Findings

| # | Sev | Finding | Classification |
|---|---|---|---|
| 1 | P1 | Mode card calls `cleanupInteractiveState` **after a successful live switch**, interrupting the turn and dropping queued work even though no restart was needed (`core/engine.go` `/mode` card case). | cc-connect wiring |
| 2 | P1 | Pi `/reasoning` clears the saved ID and history. Pi already launches with `--session-id` + `--thinking` in both JSON and RPC modes. | missing wiring |
| 3 | P1 | `/provider` resets context even when the selected provider is already active; management activation calls `resetAllSessions` and erases **all** session records, including inactive ones. | missing no-op / overly broad reset |
| 4 | P2 | Mode card fallback clears ID/history unless `ResumeModeApplier` is implemented. Several adapters already carry mode on resume: Claude, Cursor, Codex, ACP/Devin, Gemini, iFlow, OpenCode, Qoder, Kimi, Antigravity, Copilot, Pi. Claude also rejects plan/auto in `SetLiveMode`, so those transitions hit the card fallback. | missing wiring |
| 5 | P2 | Slash `/new` clears the previous session record before creating the new one; the `/new` card only cleans up and calls `NewSession`, preserving the old record. `/switch` back therefore loses local history for agents without `HistoryProvider`. | handler asymmetry |
| 6 | P2 | Workspace `/model` calls `SetModel` only; `StartSession` then lets the active provider's model override it (Gemini, iFlow, OpenCode, Copilot; Antigravity also ignores model at launch). | state precedence |
| 7 | P2 | Model card cleans up even when validation/persistence fails; slash `/model` returns before cleanup on failure. A rejected/stale card can stop a healthy session. | handler ordering |
| 8 | P2 | ACP has live model RPCs (`session/set_config_option`, `session/set_model`) but core has no live-model capability, so `/model` restarts and can lose a non-resumable conversation. | missing live capability |
| 9 | P2 | `/workdir` clears context even when the effective directory is unchanged (`/workdir .`, selecting current dir, reset to current base). | missing no-op |
| 10 | P2 | Reasonix never acquires a backend resume identity (`CurrentSessionID` stays empty), so any process recreation POSTs `/new`; mode is also stored but never sent to the server. | adapter resume identity / mode not wired |

## Fix batches

### Batch A — mode/reasoning continuity (P1 + P2)

1. **Mode card live success**: return immediately after
   `applyLiveModeChange` succeeds; do **not** call
   `cleanupInteractiveState`. This matches the slash path.
2. **Shared mode selection helper** used by both slash `/mode` and the card
   action:
   - set mode, try live switch;
   - live success → no cleanup, reply live;
   - otherwise cleanup; if `ResumeModeApplier` → keep saved ID/history and
     reply on-resume;
   - otherwise clear ID/history and reply new-session.
   This removes slash/card divergence.
3. **Implement `ResumeModeApplier`** (`AppliesModeOnResume() bool { return true }`)
   for agents whose start/resume path already applies `a.mode`:
   `claudecode`, `cursor`, `codex`, `devin` (Devin-specific, after confirming
   its ACP server honors `session/set_mode`), `gemini`, `iflow`, `opencode`,
   `qoder`, `kimi`, `antigravity`, `copilot`, `pi`.
   Do **not** add a blanket applier to the generic `acp` adapter; servers that
   do not support `session/set_mode` need a separate capability check.
   DSH already implements it; do not change its log-rewrite path.
4. **Pi `/reasoning`**: implement `ResumeReasoningApplier`; both JSON and RPC
   builders already combine `--session-id` with `--thinking`.
5. **Tests**:
   - `TestModeCard_LiveSuccessDoesNotCloseSession` (zero Close, queued work kept).
   - Table-driven slash/card: live success, live failure + resume applier,
     genuinely non-resumable agent.
   - Per-agent tests: after `SetMode("yolo")`, `StartSession(ctx, "resume-id")`
     passes the saved ID and the new mode to the launch/RPC config.
   - `TestPiReasoningSwitch_PreservesConversation` for JSON and RPC.
   - CUJ `TestCUJ_C7_ModeSwitchPreservesHistory`: chat → `/mode yolo` via
     `ReceiveMessage` → follow-up recall → assert same session ID/history.

### Batch B — no-op and ordering fixes (P2, low risk)

6. **Provider no-op**: if `GetActiveProvider()` already equals the target,
   reply "already active" and return without cleanup/reset. Apply to slash
   and card paths.
7. **Workdir no-op**: normalize the effective target; if it equals the
   current workdir, return without cleanup/reset. Genuine directory changes
   keep the existing new-session policy.
8. **Model card failure**: validate and persist **before** cleanup; on any
   error, leave the live session and queued work untouched. Match slash
   ordering.
9. **`/new` slash archive**: remove the `SetAgentSessionID("")` +
   `ClearHistory()` of the old record; mirror the card path so `/switch`
   back can restore history. Keep `/cancel`'s documented reset.
10. **Workspace model precedence**: when `persistConfig=false`, update the
    active provider's in-memory model (or pin the explicit model) so
    `StartSession` does not override it. Cover Gemini, iFlow, OpenCode,
    Copilot. Antigravity's ignored `--model` stays a documented adapter
    limitation.
11. **Tests**: same-provider selection keeps ID/history; unchanged workdir
    keeps ID/history; failed model card keeps session and queued work;
    `/new` + `/switch` shows prior history; workspace `/model` launches with
    the selected model.

### Batch C — deferred, document only

12. Compatible provider transition with resume (cross-provider transcript
    compatibility is not established); management activation should update
    defaults without wiping inactive sessions.
13. ACP `LiveModelSwitcher` (`session/set_config_option`/`session/set_model`).
14. Reasonix backend resume identity and mode application.

Do not implement Batch C in this change unless Batch A/B tests are green and
there is time; record it in the plan/PR instead.

## Validation

- `go test ./core/ -run 'TestCUJ|TestMode|TestProvider|TestWorkdir|TestModel'`
- `go test ./agent/<changed packages>`
- `go test ./core/...` and `go build ./...`
- `bash .github/tests/upstream-sync.sh`
- Real Feishu flow on the codex-bot app (app-server backend):
  1. chat a unique token;
  2. `/mode yolo` (slash) and mode card;
  3. ask for the token back;
  4. assert same session and token recall, no context loss.
