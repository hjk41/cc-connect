# Contributing to cc-connect

[中文](#为-cc-connect-做贡献) | [English](#contributing-to-cc-connect)

Welcome to **hjk41/cc-connect**, a maintained fork of [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect). We focus on Codex, Cursor, Devin, and DeepSeek Harness (dsh), and welcome fixes, tests, documentation, and improvements to all supported agents and platforms. English and Chinese issues and PRs are welcome. Please keep discussions respectful and focused on the work.

## Before You Open An Issue Or PR

1. Search first.
Check this fork's [Issues](https://github.com/hjk41/cc-connect/issues) and [Pull requests](https://github.com/hjk41/cc-connect/pulls) for duplicates or related discussion before starting new work.

2. Try this fork's latest `main` or a fork release.
The existing npm package, Homebrew formula, and built-in updater use upstream builds. Please include the commit from your fork checkout (`git rev-parse --short HEAD`) so we can identify the code you tested.

## Development Setup

Use Go 1.25+, Node.js 24, and pnpm 10. From the repository root:

```bash
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
go build ./...
go vet ./...
go test ./...
```

Building the web assets first is required by Go's embedded web UI. For Go-only work, `go test -tags no_web ./...` is a useful quick check; CI also builds and tests the web UI. On macOS, use `TMPDIR=/private/tmp` if a test compares canonical temporary paths.

## Writing A Helpful Issue

Please include as much of the following as possible:

- Version: `cc-connect --version`, npm tag, or release asset
- Environment: OS, installation method, agent type, and platform
- Reproduction steps: the smaller the repro, the better
- Expected behavior vs. actual behavior
- Logs or errors, with secrets redacted
- Optional analysis or a proposed fix

For security vulnerabilities, use [private security reporting](SECURITY.md). Maintainers review contributions as availability allows.

## Pull Requests

- Follow the repo guidance in [`CLAUDE.md`](./CLAUDE.md) and [`AGENTS.md`](./AGENTS.md).
- Set the PR base repository to **`hjk41/cc-connect`** and branch to **`main`**; GitHub may otherwise default to upstream.
- Keep changes focused. New features need unit tests; bug fixes need a regression test that fails before the fix and passes after it.
- Run local validation before submitting. At minimum:

```bash
go test ./...
```

- Run `go test ./core/ -run TestCUJ` for engine, session, scheduling, or command-handler changes; run `go test -race ./...` for concurrency changes.
- For multi-step user behavior, update the relevant CUJ in `core/cuj_test.go` and assert what users see.
- Format changed Go files with `gofmt`. New user-facing strings need all supported translations in `core/i18n.go`.
- Call out breaking changes explicitly in the PR description.
- Update docs or examples when behavior or configuration changes.
- If you are fixing an issue, link it in the PR body with `Closes #<number>` when appropriate.

## Upstream Sync and Releases

We aim to review and merge upstream daily. The sync workflow preserves upstream history in a merge PR; maintainers resolve conflicts and require passing checks. Keep fork-specific changes easy to review and avoid unrelated rewrites that complicate future merges.

Fork releases use tags such as `v1.5.1-fork.1` and are published when ready. See [fork releases](https://github.com/hjk41/cc-connect/releases) and the [maintenance guide](docs/maintaining.md); there is no fixed release or review-time guarantee.

## Community

- Fork: [Issues](https://github.com/hjk41/cc-connect/issues), [Pull requests](https://github.com/hjk41/cc-connect/pulls)
- Upstream community: [Discord](https://discord.gg/kHpwgaM4kq), [Telegram](https://t.me/+odGNDhCjbjdmMmZl)

---

# 为 cc-connect 做贡献

欢迎参与 **hjk41/cc-connect**，这是持续维护的 [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect) fork。我们重点改进 Codex、Cursor、Devin 和 DeepSeek Harness（dsh），也欢迎所有 Agent、平台、测试与文档的贡献。Issue 和 PR 均可使用中文或英文，请尊重彼此并围绕具体问题讨论。

## 提交 Issue 或 PR 之前

1. 先搜索。
先查看本 fork 的 [Issues](https://github.com/hjk41/cc-connect/issues) 和 [Pull requests](https://github.com/hjk41/cc-connect/pulls)，避免重复劳动，也方便在已有讨论里继续跟进。

2. 先试本 fork 最新的 `main` 或 fork Release。
现有 npm 包、Homebrew formula 和内置自更新使用的是上游构建。请附上 fork 源码的提交号（`git rev-parse --short HEAD`），方便定位测试版本。

## 开发环境

需要 Go 1.25+、Node.js 24 和 pnpm 10，在仓库根目录执行：

```bash
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
go build ./...
go vet ./...
go test ./...
```

Go 会嵌入 Web UI，需先构建前端。仅修改 Go 时可以用 `go test -tags no_web ./...` 快速检查，CI 仍会构建并测试 Web UI。macOS 上遇到临时目录规范路径比较问题时，可设置 `TMPDIR=/private/tmp`。

## 如何提交高质量 Issue

建议尽量包含以下信息：

- 版本号：`cc-connect --version`、npm 标签或 release 资源名
- 环境：操作系统、安装方式、Agent 类型、平台类型
- 复现步骤：越小越好
- 预期行为和实际行为
- 日志或报错，注意打码敏感信息
- 可选的原因分析或修复思路

安全漏洞请通过[私密安全反馈](SECURITY.md)提交。维护者会根据可用时间处理反馈和贡献。

## Pull Request

- 请遵循仓库中的 [`CLAUDE.md`](./CLAUDE.md) 和 [`AGENTS.md`](./AGENTS.md)。
- PR 的目标仓库请选择 **`hjk41/cc-connect`**，目标分支为 **`main`**；GitHub 可能默认选中上游仓库。
- 每个 PR 聚焦一个改进；新功能需要单元测试，Bug 修复需要修复前失败、修复后通过的回归测试。
- 提交前请先做本地验证，至少执行：

```bash
go test ./...
```

- 如果包含 breaking change，请在 PR 描述中明确说明。
- 引擎、会话、定时任务或命令处理器改动需执行 `go test ./core/ -run TestCUJ`；并发改动需执行 `go test -race ./...`。
- 涉及多步用户流程时，请更新 `core/cuj_test.go` 中对应的 CUJ，检查用户实际看到的行为。
- 修改的 Go 文件需经过 `gofmt`；新增用户可见文案需在 `core/i18n.go` 中提供全部语言翻译。
- 如果改动影响行为或配置，请同步更新文档或示例。
- 如果是在修复 issue，适合时请在 PR 描述中使用 `Closes #<编号>` 关联。

## 上游同步与发版

我们计划每天检查并合并上游。同步工作流会保留上游历史并创建合并 PR，由维护者解决冲突并检查 CI。请保持 fork 改动易于审阅，减少与目标无关的大范围重写，方便后续持续合并上游。

本 fork 使用 `v1.5.1-fork.1` 这样的标签，在准备就绪后发布。请以[本 fork 的 Releases](https://github.com/hjk41/cc-connect/releases) 和[维护指南](docs/maintaining.md)为准，不承诺固定发版或审核时限。

## 社区

- 本 fork：[Issues](https://github.com/hjk41/cc-connect/issues)、[Pull requests](https://github.com/hjk41/cc-connect/pulls)
- 上游社区：[Discord](https://discord.gg/kHpwgaM4kq)、[Telegram](https://t.me/+odGNDhCjbjdmMmZl)
