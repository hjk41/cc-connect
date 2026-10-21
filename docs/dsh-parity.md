# DeepSeek Harness (dsh) parity

`type = "dsh"` runs `dsh --profile acp` and embeds the shared ACP agent.
Auth is the process environment variable `DEEPSEEK_API_KEY`. cc-connect
forwards that value and does not store or log it.

## Compared with Claude Code

| Capability | Claude Code | dsh |
|---|---|---|
| Chat over the bridge | Native CLI stream | ACP `session/prompt` |
| Resume an existing session | CLI session id | `session/resume` when `loadSession` is false and `sessionCapabilities.resume` is present. `session/load` is unchanged for agents that advertise it. |
| Close | Process exit | `session/close` when advertised, then the process is killed |
| Model switch | CLI model flag | ACP `session/set_config_option`. Values may be opaque JSON arrays such as `["deepseek-official","deepseek-flash"]`. `/model` accepts that opaque value, the option display name, or the inner id (`deepseek-flash`). The footer uses the option name. |
| Model list | CLI | Live `configOptions`, else a short probe (`session/new` in a temp directory, `session/close`, delete that session under `DSH_HOME`), else `deepseek-flash`, `deepseek-v4-pro`, `deepseek-v4-flash` |
| Reasoning effort | Agent-specific | `thought_level`: `off`, `low`, `high`, `max`. Aliases: `medium`→`high`, `xhigh`→`max`, `minimal`/`none`→`off`. Applied after the model. |
| Permission mode | Live where the CLI allows it | `DSH_PERMISSION_MODE` seeds a fresh session: `default`→`workspace-write` (ask), `plan`→`read-only`, `yolo`→`danger-full-access`. There is no `session/set_mode`. A resumed session keeps the permission facts in its log, so before `session/resume` cc-connect appends `permission/preset`, `sandbox/mode`, and `approval/policy` when they differ. `/mode` applies on the next message of the same conversation. If the log cannot be updated, the user is told to use `/new`. |
| Provider / base URL | Provider config | Active provider `api_key` becomes `DEEPSEEK_API_KEY`. `base_url` becomes `DEEPSEEK_BASE_URL`. Other provider env vars are forwarded. |
| Session list | CLI | ACP `session/list` merged with on-disk metadata, filtered to the current `work_dir`. Probe directories are excluded. |
| History and title | Transcript | `$DSH_HOME/sessions/<cwd-slug>/<id>/session.vN.jsonl.zstd`. Title is the latest `session/title`, otherwise the first user prompt. Runtime-context and skill-catalog messages are skipped. |
| Delete | CLI | Removes that session directory only after the log header id matches and `session.lock` is not held by a live process. Symlinked session directories are refused. |
| Context usage | CLI result usage | ACP `usage_update` (`used` / `size`) |
| `/usage` | Provider quota | `GET https://api.deepseek.com/user/balance` with a short timeout. The balance is rendered as a balance, not as the account id. |
| Memory | `CLAUDE.md` | Project `<work_dir>/AGENTS.md` and global `$DSH_HOME/AGENTS.md`. dsh also reads `CLAUDE.md` and ancestor overlays on its own. |
| Skills | `.claude/skills` | Existing dirs among `<project>/.dsh/skills`, `<project>/.agents/skills`, `$DSH_HOME/skills`, and `$DSH_AGENTS_HOME/skills` |
| Doctor | `claude --version` | `dsh --version` |
| System prompt | Native channel | ACP has no system-prompt channel. `HasSystemPromptSupport` is false, so the engine writes cc-connect instructions into `AGENTS.md`. Platform formatting is prefixed onto the first prompt of a newly created session only. |
| Images | Native | `promptCapabilities.image` is false. The ACP layer saves the file and sends the path. |
| `/compact` | Native slash command | Not advertised. The ACP profile has no slash-command channel (`dsh-command-compact` is a chat-UI command). dsh compacts the context on its own. |

## Deferred

- **`ContextCompressor` / `/compact`.** Sending the text `/compact` as an ACP prompt does not run dsh's compact command. Advertising the interface would tell users a command that does not work.
- **Live `session/set_mode`.** dsh returns method-not-found. A fresh session is seeded from `DSH_PERMISSION_MODE`. A resumed session is updated by appending permission facts to its log before `session/resume`.
- **Native image blocks and a native system prompt.** The ACP initialize result does not offer them.
- **Windows lock detection.** dsh uses a named semaphore there and does not leave a reliable lock file. A missing `session.lock` is treated as unlocked.
