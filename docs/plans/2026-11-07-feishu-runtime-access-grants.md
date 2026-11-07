# Feishu runtime access grants via `/config set`

Status: implemented, deployed, Feishu-verified (2026-11-07)
Date: 2026-11-07
Owner: Codex implementation, dsh review/acceptance

## Goal

Allow an admin to change Feishu access grants from chat without editing
`config.toml` or restarting the process:

```text
/config set allow_from ou_xxx
/config set allow_from @user
/config set allow_all_members $this
/config set allow_all_members oc_xxx
```

`allow_chat` is explicitly out of scope and must not be changed by these
commands. The existing static `allow_chat` filter continues to apply.

## Semantics

- `allow_from` maps to the existing Feishu `allow_from` option.
- `allow_all_members` maps to the existing `allow_group_members` option.
  The command name is the user-facing alias; config keeps the existing key.
- Plain values replace the current list.
- `+value` appends; `-value` removes. `none` clears.
- Comma-separated values are supported.
- `$this` resolves to the current group chat ID and is valid only in a group.
- `@user` resolves to the mentioned user's Feishu open_id. The bot mention
  itself is ignored; multiple non-bot mentions are all included.
- Values that cannot be resolved (DM `$this`, `@user` without mention metadata,
  wildcard for `allow_all_members`) produce an actionable error and do not
  mutate state.
- The command is admin-only (`admin_from`), because it changes access control.

## Architecture

### Core message metadata

Add to `core.Message`:

```go
ChatType string
Mentions []Mention

type Mention struct {
    UserID string
    Name   string
}
```

Feishu populates both from the verified event. Mentions without an open_id are
skipped. The bot's own mention is excluded.

### Runtime updater

Define an optional capability in `core`:

```go
type AccessListUpdater interface {
    AccessLists() (allowFrom, allowGroupMembers string)
    SetAccessLists(allowFrom, allowGroupMembers string) error
}
```

Feishu implements it. Guard the two fields with a mutex because messages and
card actions read them concurrently. `allow_chat` stays immutable.

`SetAccessLists` validates `allow_group_members` (no `*`), trims values, and
updates both fields atomically. Empty values are allowed.

### `/config` handling

Handle `allow_from` and `allow_all_members` in `cmdConfig` before the generic
`configItems` path:

- `get`: show the current value from the platform updater.
- `set`: parse `+`/`-`/`none`, resolve `$this`/mentions, update the platform,
  then persist through a new engine callback.
- Unsupported platform: reply that runtime access grants are Feishu-only.
- Do not add these keys to the generic display config card.

Admin gate: extend `isPrivilegedCommandInvocation` so only
`/config set allow_from ...` and `/config set allow_all_members ...` require
admin. Other `/config set` keys keep their current behavior.

### Persistence

Add an engine callback:

```go
type PlatformAccessSaveFunc func(platformName, allowFrom, allowGroupMembers string) error
```

Wire it in `cmd/cc-connect/main.go` to `config.SaveProjectSettings` with new
`PlatformAllowGroupMembers` map support. The config writer updates
`[projects.platforms.options]` keys:

- `allow_from`
- `allow_group_members`

The running platform is already updated, so no restart is needed. The write is
best-effort from the command's perspective: if persistence fails, report the
error but keep the live grant (or roll back? Prefer report and keep live state;
document).

### Startup

Feishu reads `allow_from` and `allow_group_members` as before. No new config
option.

## Tests

- Feishu `SetAccessLists` updates both fields; `isSenderAllowed` sees changes
  immediately; `allow_chat` remains unchanged.
- Concurrent reads under `-race`.
- Validation: wildcard group grant rejected; whitespace trimmed; empty clears.
- `$this` resolves group; rejected in p2p.
- `@user` resolves from mention metadata; bot mention ignored; multiple users.
- `+` append, `-` remove, `none` clear.
- Admin gate: non-admin cannot set either key; admin can.
- Persistence callback receives the full pair and writes both config keys.
- Existing `/config` display keys and Feishu authorization tests stay green.

## Rollout

1. Run `gofmt`, `go test ./core/ ./platform/feishu/ ./config/`, `go build ./...`,
   `go vet`.
2. Build and install the binary.
3. Ensure `admin_from` includes the operator in the target project; otherwise
   the command is intentionally blocked.
4. Restart the target bot once to load the new command.
5. Feishu E2E:
   - `/config get allow_from`
   - `/config set allow_all_members $this`
   - `/config set allow_from @member`
   - verify an unauthorized member becomes authorized without restart
   - `/config set allow_from -@member` revokes
   - verify config file persistence
6. Run CI/CodeQL and merge.

## Verification (2026-11-07)

Code:
- `go test ./core/ ./platform/feishu/ ./config/` pass.
- `go test -race ./platform/feishu/ -run 'TestSetAccessLists|TestAccessLists|TestCoreMentions|...'` pass.
- `go build ./...` and `go vet ./core/ ./platform/feishu/ ./config/` pass.

Feishu:
- `/config get allow_from` returned the current owner ID.
- `/config set allow_all_members $this` in a DM returned the expected
  "requires a group chat" error; in a group it resolved the current `oc_...`
  and persisted it to `allow_group_members`.
- `/config set allow_from +ou_test_user` added the ID, `/config get` reflected
  it immediately, and `-ou_test_user` removed it; the config file changed
  without a restart.
- `/config set allow_from +@self` with a Feishu mention resolved the mentioned
  open_id.
- Before `admin_from` was configured, the command was rejected with the
  admin-required message; after moving `admin_from` under `[[projects]]`, the
  same command executed.
- `allow_chat` was not changed by any command.

Deployment:
- Binary `v1.5.7-feishu-auth.1` installed; `com.cc-connect.codex-bot`
  restarted once to load the new command and `admin_from`.

## Non-goals

- Changing `allow_chat` from the command.
- Per-group user mapping (`oc_group:ou_user`).
- Card-based grant management.
- Non-Feishu runtime allowlists.
