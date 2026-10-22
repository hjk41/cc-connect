# CC-Connect

[English](README.md) | [中文](README.zh-CN.md)

[![CI](https://github.com/hjk41/cc-connect/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/hjk41/cc-connect/actions/workflows/ci.yml)
[![CodeQL](https://github.com/hjk41/cc-connect/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/hjk41/cc-connect/actions/workflows/codeql.yml)
[![Upstream Sync](https://github.com/hjk41/cc-connect/actions/workflows/upstream-sync.yml/badge.svg?branch=main)](https://github.com/hjk41/cc-connect/actions/workflows/upstream-sync.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/hjk41/cc-connect)](https://github.com/hjk41/cc-connect/blob/main/go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/hjk41/cc-connect/blob/main/LICENSE)

This is the maintained **[hjk41/cc-connect](https://github.com/hjk41/cc-connect)** fork of **[chenhg5/cc-connect](https://github.com/chenhg5/cc-connect)**. It follows upstream `main` daily and focuses on Codex, Cursor, Devin, and DeepSeek Harness (dsh), improving conversation continuity, progress reporting, model selection, and permission handling. A daily DSH task syncs upstream, resolves conflicts, and keeps CI green.

CC-Connect connects AI coding agents running on your computer or server to messaging apps. You can ask an agent to inspect code, make changes, run commands, and continue work on a project from a chat conversation.

```text
Feishu / Telegram / Discord / Slack / …
                    ↕
                CC-Connect
                    ↕
Codex / Claude Code / Cursor / Devin / dsh / …
                    ↕
              Your project
```

## What it does

- Routes messages and attachments to an agent, then sends replies, progress, and tool results back to chat.
- Supports session history and resume, model and reasoning-effort selection, permission prompts, and skills, depending on the agent and platform.
- Connects multiple projects and bots, each with its own working directory and configuration.
- Provides a web interface for configuration and chat, plus scheduled tasks and background service support.

## Get started

1. Install and authenticate the coding agent you want to use.
2. Follow [INSTALL.md](INSTALL.md) to build this fork and configure a project, working directory, and messaging bot. Use [config.example.toml](config.example.toml) as a configuration reference.
3. Start `cc-connect`, then send a message to your bot. Use `/help` to see available commands.

Use a source build or an available [release from this repository](https://github.com/hjk41/cc-connect/releases) to get this fork's changes. The existing npm package, Homebrew formula, and built-in updater use upstream distribution channels.

## Documentation

- [Usage guide](docs/usage.md)
- Platform setup: [Feishu](docs/feishu.md) · [Telegram](docs/telegram.md) · [Discord](docs/discord.md) · [Slack](docs/slack.md)
- Agent details: [Cursor](docs/cursor-parity.md) · [dsh](docs/dsh-parity.md)
- [Contributing](CONTRIBUTING.md) · [Maintenance](docs/maintaining.md) · [Security](SECURITY.md)
- [Issues](https://github.com/hjk41/cc-connect/issues)

## License

[MIT](LICENSE). Built on the work of the upstream cc-connect authors and contributors.
