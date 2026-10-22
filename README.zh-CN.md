# CC-Connect

[English](README.md) | [中文](README.zh-CN.md)

[![CI](https://github.com/hjk41/cc-connect/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/hjk41/cc-connect/actions/workflows/ci.yml)
[![CodeQL](https://github.com/hjk41/cc-connect/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/hjk41/cc-connect/actions/workflows/codeql.yml)
[![上游同步](https://github.com/hjk41/cc-connect/actions/workflows/upstream-sync.yml/badge.svg?branch=main)](https://github.com/hjk41/cc-connect/actions/workflows/upstream-sync.yml)
[![Go 版本](https://img.shields.io/github/go-mod/go-version/hjk41/cc-connect)](https://github.com/hjk41/cc-connect/blob/main/go.mod)
[![许可证: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/hjk41/cc-connect/blob/main/LICENSE)

CC-Connect 把运行在电脑或服务器上的 AI 编程助手接入聊天软件。你可以通过聊天让助手查看代码、修改项目、执行命令，并继续之前的工作。

```text
飞书 / Telegram / Discord / Slack / …
                    ↕
                CC-Connect
                    ↕
Codex / Claude Code / Cursor / Devin / dsh / …
                    ↕
                 你的项目
```

## 能做什么

- 将消息和附件交给编程助手，把回复、执行进度和工具结果发回聊天。
- 支持会话历史与恢复、模型和思考强度切换、权限确认及 skills，具体能力取决于助手和聊天平台。
- 同时连接多个项目和机器人，分别配置工作目录和运行参数。
- 提供用于配置和聊天的 Web 界面，以及定时任务和后台服务支持。

## 这个仓库的定位

这是基于 **[chenhg5/cc-connect](https://github.com/chenhg5/cc-connect)** 持续维护的 **[hjk41/cc-connect](https://github.com/hjk41/cc-connect)** 分支。

我们重点改进 Codex、Cursor、Devin 和 DeepSeek Harness（dsh）的使用体验，包括连续对话、进度展示、模型选择和权限处理。每日工作流会创建或更新上游同步 PR，经审查和测试后合入。

## 开始使用

1. 安装并登录你要使用的 AI 编程助手。
2. 按照 [INSTALL.md](INSTALL.md) 构建本分支，配置项目、工作目录和聊天机器人。配置项可参考 [config.example.toml](config.example.toml)。
3. 启动 `cc-connect`，向机器人发送消息；输入 `/help` 查看可用命令。

要使用本分支的改进，请从源码构建，或使用[本仓库发布的版本](https://github.com/hjk41/cc-connect/releases)。现有 npm 包、Homebrew 配方和内置更新器使用上游发布渠道。

## 文档

- [使用指南](docs/usage.zh-CN.md)
- 平台接入：[飞书](docs/feishu.md) · [Telegram](docs/telegram.md) · [Discord](docs/discord.md) · [Slack](docs/slack.md)
- 助手说明：[Cursor](docs/cursor-parity.md) · [dsh](docs/dsh-parity.md)
- [参与贡献](CONTRIBUTING.md) · [维护说明](docs/maintaining.md) · [安全政策](SECURITY.md)
- [问题反馈](https://github.com/hjk41/cc-connect/issues)

## 许可证

[MIT](LICENSE)。本项目基于上游 cc-connect 作者和贡献者的工作。
