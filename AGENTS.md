# CC-Connect Development Guide

## Project Overview

CC-Connect is a bridge that connects AI coding agents (Claude Code, Codex, Gemini CLI, Cursor, etc.) with messaging platforms (Feishu/Lark, Telegram, Discord, Slack, DingTalk, WeChat Work, QQ, LINE). Users interact with their coding agent through their preferred messaging app.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   cmd/cc-connect                │  ← entry point, CLI, daemon
├─────────────────────────────────────────────────┤
│                     config/                     │  ← TOML config parsing
├─────────────────────────────────────────────────┤
│                      core/                      │  ← engine, interfaces, i18n,
│                                                 │     cards, sessions, registry
├──────────────────────┬──────────────────────────┤
│     agent/           │      platform/           │
│  ├── claudecode/     │  ├── feishu/             │
│  ├── codex/          │  ├── telegram/           │
│  ├── cursor/         │  ├── discord/            │
│  ├── gemini/         │  ├── slack/              │
│  ├── iflow/          │  ├── dingtalk/           │
│  ├── opencode/       │  ├── wecom/              │
│  ├── acp/            │  ├── qq/                 │
│  └── qoder/          │  ├── qqbot/              │
│                      │  ├── line/               │
│                      │  ├── weibo/              │
│                      │  └── cloud-web/          │
├──────────────────────┴──────────────────────────┤
│                     daemon/                     │  ← systemd/launchd service
└─────────────────────────────────────────────────┘
```

### Key Design Principles

**`core/` is the nucleus.** It defines all interfaces (`Platform`, `Agent`, `AgentSession`, etc.) and contains the `Engine` that orchestrates message flow. The core package must **never** import from `agent/` or `platform/`.

**Plugin architecture via registries.** Agents and platforms register themselves through `core.RegisterAgent()` and `core.RegisterPlatform()` in their `init()` functions. The engine creates instances via `core.CreateAgent()` / `core.CreatePlatform()` using string names from config.

**Dependency direction:**
```
cmd/ → config/, core/, agent/*, platform/*
agent/*   → core/   (never other agents or platforms)
platform/* → core/  (never other platforms or agents)
core/     → stdlib only (never agent/ or platform/)
```

### Core Interfaces

- **`Platform`** — messaging platform adapter (Start, Reply, Send, Stop)
- **`Agent`** — AI coding agent adapter (StartSession, ListSessions, Stop)
- **`AgentSession`** — a running bidirectional session (Send, RespondPermission, Events)
- **`Engine`** — the central orchestrator that routes messages between platforms and agents

Optional capability interfaces (implement only when needed):
- `CardSender` — rich card messages
- `InlineButtonSender` — inline keyboard buttons
- `ProviderSwitcher` — multi-model switching
- `DoctorChecker` — agent-specific health checks
- `AgentDoctorInfo` — CLI binary metadata for diagnostics

## Development Rules

### 1. No Hardcoding Platform or Agent Names in Core

The `core/` package must remain agnostic. Never write `if p.Name() == "feishu"` or `CreateAgent("claudecode", ...)` in core. Use interfaces and capability checks instead:

```go
// BAD — hardcodes platform knowledge in core
if p.Name() == "feishu" && supportsCards(p) {

// GOOD — capability-based check
if supportsCards(p) {
```

```go
// BAD — hardcodes agent type
agent, _ := CreateAgent("claudecode", opts)

// GOOD — derives from current agent
agent, _ := CreateAgent(e.agent.Name(), opts)
```

### 2. Prefer Interfaces Over Type Switches

When behavior differs across platforms/agents, define an optional interface in core and let implementations opt in:

```go
// In core/
type AgentDoctorInfo interface {
    CLIBinaryName() string
    CLIDisplayName() string
}

// In agent/claudecode/
func (a *Agent) CLIBinaryName() string  { return "claude" }
func (a *Agent) CLIDisplayName() string { return "Claude" }

// In core/ — query via interface, fallback gracefully
if info, ok := agent.(AgentDoctorInfo); ok {
    bin = info.CLIBinaryName()
}
```

### 3. Configuration Over Code

- Features that may vary per deployment should be configurable in `config.toml`
- Use `map[string]any` options for agent/platform factories to stay flexible
- Add new config fields with sensible defaults so existing configs don't break

### 4. High Cohesion, Low Coupling

- Each `agent/X/` package is self-contained: it handles process lifecycle, output parsing, and session management for agent X
- Each `platform/X/` package is self-contained: it handles API connection, message receiving/sending, and card rendering for platform X
- Cross-cutting concerns (i18n, cards, streaming, rate limiting) live in `core/`

### 5. Error Handling

- Always wrap errors with context: `fmt.Errorf("feishu: reply card: %w", err)`
- Never silently swallow errors; at minimum log them with `slog.Error` / `slog.Warn`
- Use `slog` (structured logging) consistently; never `log.Printf` or `fmt.Printf` for runtime logs
- Redact tokens/secrets in error messages using `core.RedactToken()`

### 6. Concurrency Safety

- Agent sessions are accessed from multiple goroutines; protect shared state with `sync.Mutex` or `atomic` types
- Use `context.Context` for cancellation propagation
- Channels should have clear ownership; document who closes them
- Prefer `sync.Once` for one-time teardown (`pendingPermission.resolve()`)

### 7. i18n

All user-facing strings must go through `core/i18n.go`:
- Define a `MsgKey` constant
- Add translations for all supported languages (EN, ZH, ZH-TW, JA, ES)
- Use `e.i18n.T(MsgKey)` or `e.i18n.Tf(MsgKey, args...)`

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `strings.EqualFold` for case-insensitive comparisons
- Avoid `init()` for anything other than platform/agent registration
- Keep functions focused; extract helpers when a function exceeds ~80 lines
- Naming: `New()` for constructors, `Get/Set` for accessors, avoid stuttering (`feishu.FeishuPlatform` → `feishu.Platform`)

## Testing

### Requirements

- All new features must include unit tests.
- **All bug fixes MUST include a regression test in the same PR.** A bug
  fix PR without a test that fails on the pre-fix code and passes on the
  fixed code will not be merged. Name regression tests so the bug is
  searchable later, e.g. `TestSwitchToAgentSession_PreservesHistory` for
  the cmdSwitch history-loss bug.
- Tests must pass before committing: `go test ./...`.
- Changes that touch a Critical User Journey (CUJ) — see
  `core/cuj_test.go` — should explicitly run `go test ./core/ -run TestCUJ`
  before opening the PR.

### Running Tests

```bash
# Full test suite
go test ./...

# Specific package
go test ./core/ -v

# Run specific test
go test ./core/ -run TestHandlePendingPermission -v

# Run Critical User Journey tests (recommended for any core/engine.go or
# core/session.go change)
go test ./core/ -run TestCUJ -v

# With race detector (CI)
go test -race ./...
```

### Test Patterns

- Use stub types for `Platform` and `Agent` in core tests (see `core/engine_test.go`).
- Test card rendering by inspecting the returned `*Card` struct, not JSON.
- For agent session tests, simulate event streams via channels.
- **For multi-step user behavior, add a CUJ test in `core/cuj_test.go`.**
  CUJ tests assert what a USER sees on the platform side across multiple
  actions (e.g. "create s1 → chat → /new s2 → /switch s1 → /history
  must show s1's content"). They exist because per-function unit tests
  can all pass while a user journey is still broken — the `/switch
  loses history` bug shipped in exactly that scenario despite full
  unit coverage of every individual function involved.

### Critical User Journeys (CUJ)

A CUJ test is a USER-perspective end-to-end scenario, not a developer-
perspective unit test. The current inventory of CUJs and their coverage
status lives in:

`projects/cc-connect/agents/qa-cursor/release-gate/CUJ-INVENTORY.md`
(in the spaceship agency workspace; the registered authoritative copy).

Rules for adding/updating CUJ tests in `core/cuj_test.go`:

1. Name: `TestCUJ_<group><id>_<short_camel_case>` (e.g. `TestCUJ_B3_SwitchPreservesHistory`).
2. Use real `SessionManager` + real `Engine`; mock only external boundaries (`Platform` sender, `Agent` process).
3. Drive the engine via `ReceiveMessage` — the same entrypoint platforms use, so engine/platform wiring is also covered.
4. Assert what the USER sees via `p.getSent()`, not internal struct fields.
5. ≥3 user actions per CUJ. A single-action assertion belongs in a unit test, not a CUJ.

When a user-reported bug maps to an existing CUJ, add a sub-case to that
CUJ rather than creating a new one.

## Selective Compilation

Each agent and platform is imported via a separate `plugin_*.go` file with a
build tag (e.g. `//go:build !no_feishu`). By default **all** agents and
platforms are compiled in.

### Include only specific agents/platforms

```bash
# Only Claude Code agent + Feishu and Telegram platforms
make build AGENTS=claudecode PLATFORMS_INCLUDE=feishu,telegram

# Multiple agents
make build AGENTS=claudecode,codex PLATFORMS_INCLUDE=feishu,telegram,discord
```

### Exclude specific agents/platforms

```bash
# Exclude some platforms you don't need
make build EXCLUDE=discord,dingtalk,qq,qqbot,line
```

### Direct build tag usage (without Make)

```bash
go build -tags 'no_discord no_dingtalk no_qq no_qqbot no_line' ./cmd/cc-connect
```

Available tags: `no_acp`, `no_claudecode`, `no_codex`, `no_copilot`, `no_cursor`, `no_dsh`, `no_gemini`,
`no_iflow`, `no_opencode`, `no_qoder`, `no_feishu`, `no_telegram`,
`no_discord`, `no_slack`, `no_dingtalk`, `no_wecom`, `no_weixin`, `no_qq`, `no_qqbot`,
`no_line`, `no_weibo`, `no_tuitui`.

## Pre-Commit Checklist

1. **Build passes**: `go build ./...`
2. **Tests pass**: `go test ./...`
3. **CUJ tests pass** (for any change in `core/engine.go`, `core/session.go`, `core/cron.go`, `core/timer.go`, or command handlers): `go test ./core/ -run TestCUJ`
4. **Bug fix has a regression test**: a new test in this PR that fails on the pre-fix code and passes on the fix.
5. **No new hardcoded platform/agent names in core**: grep for platform names in `core/*.go`.
6. **i18n complete**: all new user-facing strings have translations for all languages.
7. **No secrets in code**: no API keys, tokens, or credentials in source files.

## Adding a New Platform

1. Create `platform/newplatform/newplatform.go`
2. Implement `core.Platform` interface (and optional interfaces as needed)
3. Register in `init()`: `core.RegisterPlatform("newplatform", factory)`
4. Create `cmd/cc-connect/plugin_platform_newplatform.go` with `//go:build !no_newplatform` tag
5. Add `newplatform` to `ALL_PLATFORMS` in `Makefile`
6. Add config example in `config.example.toml`
7. Add unit tests

## Adding a New Agent

1. Create `agent/newagent/newagent.go`
2. Implement `core.Agent` and `core.AgentSession` interfaces
3. Register in `init()`: `core.RegisterAgent("newagent", factory)`
4. Create `cmd/cc-connect/plugin_agent_newagent.go` with `//go:build !no_newagent` tag
5. Add `newagent` to `ALL_AGENTS` in `Makefile`
6. Optionally implement `AgentDoctorInfo` for `cc-connect doctor` support
7. Add config example in `config.example.toml`
8. Add unit tests

## macOS 本机安装与更新（必须遵守）

更新本机 CC-Connect 可执行文件（例如 `~/.cc-connect/bin/cc-connect`）时，**禁止直接覆盖已安装文件，必须先写入全新的临时文件，再用 rename 原子替换**。

macOS 会在内核中缓存与文件对象关联的代码签名信息。原地覆盖可能保留旧 inode，使新内容与旧缓存不匹配，导致磁盘签名校验通过但执行仍被 `SIGKILL`，崩溃原因显示 `Code Signature Invalid` / `OS_REASON_CODESIGNING`。2026-11-16 本机 Devinbot 已实际遇到此故障。

必须遵循以下顺序：

1. 在目标安装目录所在的**同一文件系统**创建全新的临时文件，或临时目录中的新文件。构建或复制只能写入这个临时路径。
2. 设置正确的执行权限，核对新文件的 SHA256、执行 `codesign --verify`，并用该临时文件运行 `--version` 确认可启动。探针日志应指向本次更新的独立路径。
3. 备份旧版本；在替换前保留原安装路径，避免先删除旧文件造成服务空窗。
4. 使用 `rename(2)` / Python `os.replace()` 一次性替换目标路径；使用 `mv` 时必须确保源与目标在同一文件系统，避免退化为跨文件系统复制。
5. 按当前任务授权恢复对应服务，避免中断其他活跃机器人；核验新进程、平台连接和实际消息收发，不能只凭旧进程仍存活宣布升级成功。

禁止使用 `cp new-binary ~/.cc-connect/bin/cc-connect`、重定向写入、`shutil.copyfile()` 等方式直接写入现有安装文件，也不要直接把已安装路径作为构建输出。允许复制到全新的临时路径；最终安装步骤必须是上述原子替换。

参考：[Apple — Updating Mac Software](https://developer.apple.com/documentation/security/updating-mac-software)。

<!-- cc-connect-instructions -->
You are running inside cc-connect, a bridge that connects you to messaging platforms.
Your normal text responses are automatically delivered to the user — just reply normally, do NOT use cc-connect send for ordinary text replies.

## Available tools
### 把生成的图片、文件、语音消息回发给用户
当你生成了需要发送给用户的本地图片或文件时,使用:

  cc-connect send --image /absolute/path/to/image.png
  cc-connect send --file /absolute/path/to/report.pdf
  cc-connect send --file /absolute/path/to/report.pdf --image /absolute/path/to/chart.png

可以重复使用 --image / --file。仅在需要把生成的附件投递到用户时使用这个命令。
如果同时使用了 --message,不要在正常回复里再说一遍完全相同的句子,因为正常回复本身也会自动发送给用户。

发送音频 (mp3/wav/m4a/ogg/opus) 或视频 (mp4/mov/webm) 片段、且希望它们以内联的原生语音气泡或视频播放器形态呈现(而不是作为普通文件下载)时,使用专用参数:

  cc-connect send --audio /absolute/path/to/clip.mp3
  cc-connect send --video /absolute/path/to/demo.mp4

这些参数会在支持原生媒体的平台上渲染为原生形态(例如飞书的语音气泡、Telegram 的语音消息)。cc-connect 会自动把音频转码为平台偏好的编码(例如飞书的 opus)。在不支持专用音视频的平台,cc-connect 会自动回退到文件附件路径以保证投递成功。当用户明确要求 audio/video 时,不要把请求降级为 --file。

当用户明确要求把文字合成为语音时,使用:

  cc-connect send --tts "要朗读的文字"

cc-connect send --tts(或 --audio)成功之后,除非用户同时要求可见的文字确认,否则只回复 NO_REPLY,避免在语音消息后再发一条文字消息。

### 定时任务:什么时候用 /cron,什么时候用 /timer

cc-connect 有两个不同的调度命令。选错会让用户感到很困惑。

  ┌──────────────────────────────┬─────────────────────────────┐
  │ 使用 cc-connect cron …       │ 使用 cc-connect timer …     │
  ├──────────────────────────────┼─────────────────────────────┤
  │ 周期性调度                    │ 单次延时 / 一次性任务         │
  │ "每天/每周/每小时"            │ "X 分钟后/小时后/明天"        │
  │ "every day/week/Monday"      │ "in 30 min", "tomorrow 9am"  │
  │ "每天早上6点总结"             │ "3 分钟后检查负载"            │
  │ 一直存在直到被删除             │ 触发后自动归档                │
  │ 通过 /cron 查看               │ 通过 /timer 查看              │
  └──────────────────────────────┴─────────────────────────────┘

告诉用户任务已安排好后,顺手告诉他们用哪个命令查看/管理:
(单次任务说"用 /timer 查看",周期任务说"用 /cron 查看")。

### 周期任务 (cron) — RECURRING
当用户让你做周期性任务时(例如"每天早上6点帮我总结GitHub trending"),用 Bash 工具执行:

  cc-connect cron add --cron "<分> <时> <日> <月> <星期>" --prompt "<任务描述>" --desc "<简短标签>"

环境变量 CC_PROJECT 和 CC_SESSION_KEY 已经设置好,你不需要传 --project 或 --session-key。

可选参数:
  --session-mode <mode>     reuse(默认)或 new-per-run(每次触发用新会话)
  --timeout-mins <n>        每次运行最长等待分钟数(默认 30,0 = 不限)
  --exec <command>          直接跑 shell 命令而不是 --prompt

示例:
  cc-connect cron add --cron "0 6 * * *" --prompt "汇总 GitHub trending 仓库并发摘要" --desc "每日 GitHub Trending"
  cc-connect cron add --cron "0 9 * * 1" --prompt "生成本周项目状态报告" --desc "每周报告"
  cc-connect cron add --cron "*/2 * * * *" --exec "ipconfig" --session-mode new-per-run --desc "每 2 分钟跑 ipconfig"

你也可以列出、检查、立即执行、修改或删除 cron 任务:
  cc-connect cron list
  cc-connect cron info <job-id> [字段]
  cc-connect cron exec <job-id>
  cc-connect cron edit <job-id> <字段> <新值>
  cc-connect cron del <job-id>

修改现有任务时,先用 `cc-connect cron info <job-id>` 看当前值,再用 `cron edit` 只改用户要求改的字段。
用 `cron exec <job-id>` 立即执行已存在的调度任务;这和创建 shell 任务时用的 `--exec <command>` 参数不同。
只有修改一个字段时,用 `cron edit` 而不是删除后重建。除非用户明确要求替换任务,不要先删再建。
常用可编辑字段:
  cron_expr     新调度,例如 "0 9 * * *"
  prompt        新任务 prompt(或 `exec` 表示 shell 命令)
  description   简短标签
  enabled       true / false(暂停而不删除)
  mute          true / false(静默所有消息)
  timeout_mins  整数分钟(0 = 不限)
完整字段列表见 `cc-connect cron edit --help`。

示例:
  cc-connect cron exec abc123
  cc-connect cron edit abc123 cron_expr "0 9 * * *"
  cc-connect cron edit abc123 enabled false
  cc-connect cron edit abc123 prompt "更新后的每日摘要任务"

### 一次性延时 (timer) — ONE-TIME DELAY
当用户让你在一段延时之后或在某个未来时刻做某事时
(例如"两小时后帮我检查PR"、"3 分钟后看下系统负载"、"明天早上 9 点提醒我"),
用 Bash 工具执行:

  cc-connect timer add --delay <时长> --prompt "<任务描述>"

重要:不要用 cron 跑单次延时。形如 "4 19 14 6 *" 的 cron 表达式
意思是"每年 6 月 14 日 19:04"而不是"这一天跑一次"。cron 没有内建"只跑一次"模式 ——
所有一次性 / 延时任务都用 timer。

时长示例:30m、2h、1h30m。或者用绝对时间:--at "2026-05-16T09:00"
不带时区的绝对时间(例如 "2026-05-16T09:00")按系统本地时区解释。用户说"明天早上9点"时用本地时间。
环境变量 CC_PROJECT 和 CC_SESSION_KEY 已经设置好。

可选参数:
  --exec <command>          直接跑 shell 命令而不是 --prompt
  --desc <text>             简短描述
  --session-mode <mode>     reuse(默认)或 new-per-run(每次跑用新会话)
  --timeout-mins <n>        每次最长等待分钟数(默认 30,0 = 不限)
  --mute                    静默所有消息(开始通知和结果)

示例:
  cc-connect timer add --delay 2h --prompt "检查 PR 状态" --desc "PR 检查"
  cc-connect timer add --delay 30m --exec "df -h" --desc "磁盘检查"
  cc-connect timer add --at "2026-05-16T09:00" --prompt "早会提醒"

你也可以列出或取消 timer:
  cc-connect timer list
  cc-connect timer del <timer-id>

### Bot 之间转发 (relay)
当需要和另一个 bot 通信(例如向另一个 AI agent 提问)时,使用:

  cc-connect relay send --to <目标项目名> "<消息>"

重要:<目标项目名> 必须是 /bind 命令输出里的 EXACT 项目名。
不要猜测或修改名字 —— 完全照搬显示值(例如 "gemini" 而不是 "gemini-bot")。

这会把消息发给目标 bot 并等待它的回复(打印到 stdout)。
会话在群聊里可见,每个 bot 维护自己的 relay 会话。

环境变量 CC_PROJECT 和 CC_SESSION_KEY 已经设置好,relay 知道用哪个群聊。
### Silent reply (suppress delivery)
If the current turn warrants no user-visible response — e.g. a scheduled trigger
found nothing worth reporting, the incoming message was an acknowledgement that
needs no reaction, or it was clearly directed at another participant — end your
reply with the token `NO_REPLY` on its own line (case-insensitive). cc-connect strips
the trailing marker before delivery:
- If the whole reply is just `NO_REPLY` (or the text becomes empty after the
  marker is stripped), nothing is delivered — no preview, no done reaction, no
  TTS. Prefer this for group-chat gate decisions where silence is the whole point.
- If you wrote reasoning before the marker, the stripped reasoning is still
  delivered as a normal reply (the marker only suppresses itself, not the
  surrounding text).
Use this sparingly; when in doubt, send a brief reply instead.

