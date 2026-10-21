# Maintaining the cc-connect fork

This guide describes automation and repository settings for [hjk41/cc-connect](https://github.com/hjk41/cc-connect). The fork follows [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect) and focuses on Codex, Cursor, Devin, and DeepSeek Harness (dsh). The Go module path stays compatible with upstream to keep regular merges manageable.

## Repository setup

The maintainer must enable the following settings in this fork:

- **Issues** for bug reports, questions, and feature requests.
- **Actions**, with default workflow permissions set to **read repository contents**, and **Allow GitHub Actions to create and approve pull requests** enabled for the sync workflow. The workflows request write access only in the jobs that need it.
- **Private vulnerability reporting**, Dependabot alerts, and dependency security updates.
- **Secret scanning and push protection** to detect supported credentials and block accidental pushes containing them.
- **Merge commits**. Upstream sync PRs must be merged using **Create a merge commit** to retain upstream ancestry. Squashing or rebasing sync PRs makes later syncs rediscover old commits.
- **Automatically delete head branches** after merging PRs. The sync branch is recreated as needed.

The `main` branch rule requires pull requests, the **CI passed** status check with an up-to-date base, and resolved review conversations; it blocks force pushes and deletions. It requires zero additional approvals so a sole maintainer can merge after checks pass. Administrator enforcement is disabled, allowing the owner to bootstrap the first workflow push. After committing and pushing the workflows, confirm CI runs and reports **CI passed**; a new check has no result before its first run. Also review CodeQL findings before merging.

The README badges refer to this fork's workflows. They acquire a result after the workflows are pushed and run; upstream release, download, star, and contributor metrics are not used as fork metrics. `CODEOWNERS` points to the fork maintainer. The MIT license file records the license already stated by the upstream README and credits cc-connect contributors collectively.

## Continuous integration

| Check | Purpose |
|-------|---------|
| Workflow lint and sync regression checks | Validate Actions YAML and exercise real temporary Git repositories for no-op syncs, merge ancestry, repeated syncs, and conflicts. |
| Web | Install from the pnpm lockfile and build/type-check the web UI once; share the generated assets with Go jobs. |
| Go (Linux, macOS, Windows) | Verify modules, build, vet, run the full test suite with race detection and coverage, and run CUJs explicitly. Linux coverage is retained as an Actions artifact. |
| Smoke and regression | Run the existing tagged suites without external agent or IM credentials. |
| CI passed | Aggregate required jobs into a stable branch-protection check; failed, cancelled, or skipped prerequisites fail the gate. |
| Performance | Run the existing benchmarks on pushes to `main` and manual CI runs. |
| CodeQL | Scan Go and JavaScript/TypeScript on pushes, PRs, and weekly. Go uses an explicit build without embedded web assets; TypeScript is analyzed from source. |
| Dependabot | Open weekly updates for GitHub Actions, Go modules, and web dependencies, grouping minor/patch updates and limiting open PRs. |

All third-party actions are pinned to commit SHAs. Dependabot maintains those pins. CI has timeouts and cancels superseded runs. Contributor PRs use `pull_request` with read-only contents permissions; automation does not run contributor code under `pull_request_target`.

Local verification after building web assets (`CI=true` skips the existing real Cursor login test, as it does on GitHub Actions):

```bash
go build ./...
go vet ./...
CI=true go test ./...
go test ./core/ -run TestCUJ
CI=true go test -p=4 -parallel=4 -race ./...
go test -tags=smoke,no_web ./tests/e2e/...
go test -tags=regression,no_web ./tests/e2e/...
bash .github/tests/upstream-sync.sh
```

For macOS temporary-path comparisons, prefix Go commands with `TMPDIR=/private/tmp`. Workflow lint is pinned in CI; run the same actionlint version locally when changing workflows.

## Daily upstream sync

[Upstream Sync](../.github/workflows/upstream-sync.yml) runs daily at **21:17 UTC / 06:17 JST the next day**, and can also be started using **Run workflow**. It is guarded to run only in `hjk41/cc-connect`; change that guard deliberately when reusing it in another fork.

The job fetches upstream `main`, skips when all upstream commits are already included, and otherwise prepares a merge on the reserved `automation/upstream-sync` branch. It creates or updates one PR targeting this fork's `main`. An unchanged upstream and base reuse the existing merge commit. The branch update uses a lease, and the workflow never pushes to `main` or merges the PR automatically.

Merge conflicts fail the run, list the conflicting paths in its summary, abort the local merge, and leave remote branches untouched. Resolve them in a local sync branch, run the checks, and open a PR. The Actions failure is the signal to investigate; there is no daily empty commit or duplicate issue.

The default `GITHUB_TOKEN` is sufficient for ordinary branch/PR creation once Actions PR creation is allowed. GitHub may put workflows for bot-created PRs into an approval-required state; a maintainer can select **Approve workflows to run**. For fully automatic PR checks, or upstream changes to workflow files, set a repository secret named **UPSTREAM_SYNC_TOKEN** using a token restricted to this fork with **Contents**, **Pull requests**, and **Workflows** write permissions. Never put it in repository files. See [GitHub's workflow-trigger rules](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow).

Review sync PRs for changes to this fork's positioning, ownership, distribution, and workflows. Keep them as merge commits. The repository does not automatically close issues or PRs solely because they are inactive.

## Fork releases

[Fork Release](../.github/workflows/release.yml) runs only for fork tags matching `v*-fork.*`, such as `v1.5.1-fork.1`, in this repository. Upstream tag names do not trigger a fork release. Choose an unused tag for a tested commit on `main`.

The workflow verifies the tag belongs to `main`, rebuilds the web UI, runs module verification, vet, race tests, CUJs, and the tagged smoke/regression suites, then cross-compiles six archives: Linux, macOS, and Windows on amd64/arm64. Archives include the binary, license, example config, and READMEs. A final job generates SHA-256 checksums and creates a **draft** GitHub release. Reruns can update a draft; published assets are not overwritten.

Before publishing the draft, install the appropriate archive on a real target, check `--version`, exercise the affected agent/platform flows, review the generated release notes, and describe the fork's changes and known limitations. Cross-compilation verifies builds for all six targets; CI executes tests natively on three runner platforms, not on every target architecture.

Source builds and this fork's release assets are the fork's installation channels. The upstream npm package, Homebrew formula, and built-in updater still use upstream distribution. A separate package namespace and fork-aware updater can be introduced in a future change.

## 维护约定（中文）

本 fork 以日常可用性和持续同步上游为目标。每天的工作流会检查上游并准备同步 PR；合入前由维护者检查 CI、CodeQL 和冲突，使用 **Create a merge commit** 保留上游历史。若同步失败，请查看 Actions 摘要；重复运行不会制造空提交或重复 PR。

新贡献请投递到 `hjk41/cc-connect:main`。不因长期没有活动而自动关闭 Issue 或 PR，不承诺固定审核或发版时限。新增功能需测试，Bug 修复需回归测试，涉及多步用户流程时更新 CUJ。

Actions 默认只读，只有同步和创建 Release 的任务申请所需写权限。默认令牌创建的同步 PR 可能需要点击批准运行工作流；若需自动触发检查或同步工作流文件，请设置上述 `UPSTREAM_SYNC_TOKEN`。fork 发版使用 `-fork.N` 标签，自动生成草稿及校验文件，维护者验证后再发布。主分支已配置 `CI passed` 必需检查，第一次提交并推送工作流后需确认其成功运行；管理员可绕过规则完成初始推送。
